package opensandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const snapshotPageSize = 200

// ListSnapshots returns all persistent snapshots managed by the lifecycle API.
func (client *client) ListSnapshots(ctx context.Context) ([]Snapshot, error) {
	var snapshots []Snapshot
	for page := 1; ; page++ {
		path := "/snapshots?page=" + strconv.Itoa(page) + "&pageSize=" + strconv.Itoa(snapshotPageSize)
		startedAt := time.Now()
		request, err := client.newAPIRequest(ctx, http.MethodGet, path, nil)
		if err != nil {
			client.logCall(ctx, "opensandbox", http.MethodGet, path, 0, startedAt, err)
			return snapshots, err
		}

		response, err := client.lifecycleHTTPClient.Do(request)
		if err != nil {
			requestErr := fmt.Errorf("list snapshots: %w", err)
			client.logCall(ctx, "opensandbox", http.MethodGet, path, 0, startedAt, requestErr)
			return snapshots, requestErr
		}

		var payload snapshotListResponse
		if response.StatusCode == http.StatusOK {
			err = json.NewDecoder(response.Body).Decode(&payload)
		} else {
			err = responseStatusError("list snapshots", response)
		}
		response.Body.Close()
		if err != nil {
			requestErr := fmt.Errorf("decode snapshot list: %w", err)
			if response.StatusCode != http.StatusOK {
				requestErr = err
			}
			client.logCall(ctx, "opensandbox", http.MethodGet, path, response.StatusCode, startedAt, requestErr)
			return snapshots, requestErr
		}

		for _, snapshot := range payload.Items {
			snapshots = append(snapshots, snapshot.model())
		}
		client.logCall(
			ctx,
			"opensandbox",
			http.MethodGet,
			path,
			response.StatusCode,
			startedAt,
			nil,
			slog.Int("snapshot_count", len(payload.Items)),
		)
		if !payload.Pagination.HasNextPage {
			return snapshots, nil
		}
	}
}

// GetSnapshot returns a persistent snapshot by ID.
func (client *client) GetSnapshot(ctx context.Context, snapshotID string) (Snapshot, error) {
	if strings.TrimSpace(snapshotID) == "" {
		return Snapshot{}, errors.New("snapshot ID is required")
	}
	path := "/snapshots/" + url.PathEscape(snapshotID)
	startedAt := time.Now()
	request, err := client.newAPIRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		client.logCall(ctx, "opensandbox", http.MethodGet, path, 0, startedAt, err)
		return Snapshot{}, err
	}
	response, err := client.lifecycleHTTPClient.Do(request)
	if err != nil {
		requestErr := fmt.Errorf("get snapshot: %w", err)
		client.logCall(ctx, "opensandbox", http.MethodGet, path, 0, startedAt, requestErr)
		return Snapshot{}, requestErr
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		requestErr := responseStatusError("get snapshot", response)
		client.logCall(ctx, "opensandbox", http.MethodGet, path, response.StatusCode, startedAt, requestErr)
		return Snapshot{}, requestErr
	}

	var payload apiSnapshot
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		decodeErr := fmt.Errorf("decode snapshot: %w", err)
		client.logCall(ctx, "opensandbox", http.MethodGet, path, response.StatusCode, startedAt, decodeErr)
		return Snapshot{}, decodeErr
	}
	model := payload.model()
	client.logCall(
		ctx,
		"opensandbox",
		http.MethodGet,
		path,
		response.StatusCode,
		startedAt,
		nil,
		slog.String("snapshot_id", model.ID),
	)
	return model, nil
}

// CreateSnapshot starts a persistent snapshot of a running lifecycle sandbox.
func (client *client) CreateSnapshot(ctx context.Context, sandboxID, name string) (Snapshot, error) {
	if strings.TrimSpace(sandboxID) == "" {
		return Snapshot{}, errors.New("sandbox ID is required")
	}
	payload, err := json.Marshal(apiCreateSnapshotRequest{Name: strings.TrimSpace(name)})
	if err != nil {
		return Snapshot{}, fmt.Errorf("encode create snapshot request: %w", err)
	}

	path := "/sandboxes/" + url.PathEscape(sandboxID) + "/snapshots"
	startedAt := time.Now()
	request, err := client.newAPIRequest(ctx, http.MethodPost, path, bytes.NewReader(payload))
	if err != nil {
		client.logCall(ctx, "opensandbox", http.MethodPost, path, 0, startedAt, err)
		return Snapshot{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.lifecycleHTTPClient.Do(request)
	if err != nil {
		requestErr := fmt.Errorf("create snapshot: %w", err)
		client.logCall(ctx, "opensandbox", http.MethodPost, path, 0, startedAt, requestErr)
		return Snapshot{}, requestErr
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		requestErr := responseStatusError("create snapshot", response)
		client.logCall(ctx, "opensandbox", http.MethodPost, path, response.StatusCode, startedAt, requestErr)
		return Snapshot{}, requestErr
	}

	var created apiSnapshot
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		decodeErr := fmt.Errorf("decode created snapshot: %w", err)
		client.logCall(ctx, "opensandbox", http.MethodPost, path, response.StatusCode, startedAt, decodeErr)
		return Snapshot{}, decodeErr
	}
	model := created.model()
	client.logCall(
		ctx,
		"opensandbox",
		http.MethodPost,
		path,
		response.StatusCode,
		startedAt,
		nil,
		slog.String("snapshot_id", model.ID),
		slog.String("sandbox_id", sandboxID),
	)
	return model, nil
}

// DeleteSnapshot deletes a persistent snapshot and its runtime artifact.
func (client *client) DeleteSnapshot(ctx context.Context, snapshotID string) error {
	if strings.TrimSpace(snapshotID) == "" {
		return errors.New("snapshot ID is required")
	}
	path := "/snapshots/" + url.PathEscape(snapshotID)
	startedAt := time.Now()
	request, err := client.newAPIRequest(ctx, http.MethodDelete, path, nil)
	if err != nil {
		client.logCall(ctx, "opensandbox", http.MethodDelete, path, 0, startedAt, err)
		return err
	}
	response, err := client.lifecycleHTTPClient.Do(request)
	if err != nil {
		requestErr := fmt.Errorf("delete snapshot: %w", err)
		client.logCall(ctx, "opensandbox", http.MethodDelete, path, 0, startedAt, requestErr)
		return requestErr
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		requestErr := responseStatusError("delete snapshot", response)
		client.logCall(ctx, "opensandbox", http.MethodDelete, path, response.StatusCode, startedAt, requestErr)
		return requestErr
	}
	client.logCall(
		ctx,
		"opensandbox",
		http.MethodDelete,
		path,
		response.StatusCode,
		startedAt,
		nil,
		slog.String("snapshot_id", snapshotID),
	)
	return nil
}
