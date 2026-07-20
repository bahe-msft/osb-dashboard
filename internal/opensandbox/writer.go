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
	"strings"
	"time"
)

// CreateSandbox creates an image-backed sandbox.
func (client *client) CreateSandbox(ctx context.Context, request CreateSandboxRequest) (Sandbox, error) {
	if strings.TrimSpace(request.Image) == "" {
		return Sandbox{}, errors.New("sandbox image is required")
	}
	if len(request.Entrypoint) == 0 {
		return Sandbox{}, errors.New("sandbox entrypoint is required")
	}
	if request.Timeout < 0 {
		return Sandbox{}, errors.New("sandbox timeout must not be negative")
	}
	if len(request.ResourceLimits) == 0 {
		return Sandbox{}, errors.New("sandbox resource limits are required")
	}

	payload := apiCreateRequest{
		Image:          apiSandboxImage{URI: request.Image},
		Entrypoint:     request.Entrypoint,
		ResourceLimits: request.ResourceLimits,
		Metadata:       request.Metadata,
	}
	if request.Timeout > 0 {
		timeoutSeconds := int(request.Timeout.Seconds())
		payload.Timeout = &timeoutSeconds
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Sandbox{}, fmt.Errorf("encode create sandbox request: %w", err)
	}

	const path = "/sandboxes"
	startedAt := time.Now()
	httpRequest, err := client.newAPIRequest(ctx, http.MethodPost, path, bytes.NewReader(body))
	if err != nil {
		client.logCall(ctx, "opensandbox", http.MethodPost, path, 0, startedAt, err)
		return Sandbox{}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")

	response, err := client.httpClient.Do(httpRequest)
	if err != nil {
		requestErr := fmt.Errorf("create sandbox: %w", err)
		client.logCall(ctx, "opensandbox", http.MethodPost, path, 0, startedAt, requestErr)
		return Sandbox{}, requestErr
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		requestErr := responseStatusError("create sandbox", response)
		client.logCall(ctx, "opensandbox", http.MethodPost, path, response.StatusCode, startedAt, requestErr)
		return Sandbox{}, requestErr
	}

	var sandbox apiSandbox
	if err := json.NewDecoder(response.Body).Decode(&sandbox); err != nil {
		decodeErr := fmt.Errorf("decode created sandbox: %w", err)
		client.logCall(ctx, "opensandbox", http.MethodPost, path, response.StatusCode, startedAt, decodeErr)
		return Sandbox{}, decodeErr
	}
	model := sandbox.model()
	model.Namespace = client.options.WorkloadNamespace
	client.logCall(
		ctx,
		"opensandbox",
		http.MethodPost,
		path,
		response.StatusCode,
		startedAt,
		nil,
		slog.String("sandbox_id", model.ID),
		slog.String("sandbox_state", model.State),
		slog.String("image", request.Image),
	)
	return model, nil
}

// PauseSandbox pauses a running lifecycle sandbox while preserving its state.
func (client *client) PauseSandbox(ctx context.Context, sandboxID string) error {
	return client.changeLifecycleSandboxState(ctx, sandboxID, "pause")
}

// ResumeSandbox resumes a paused lifecycle sandbox.
func (client *client) ResumeSandbox(ctx context.Context, sandboxID string) error {
	return client.changeLifecycleSandboxState(ctx, sandboxID, "resume")
}

func (client *client) changeLifecycleSandboxState(ctx context.Context, sandboxID, action string) error {
	if strings.TrimSpace(sandboxID) == "" {
		return errors.New("sandbox ID is required")
	}
	path := "/sandboxes/" + url.PathEscape(sandboxID) + "/" + action
	startedAt := time.Now()
	request, err := client.newAPIRequest(ctx, http.MethodPost, path, nil)
	if err != nil {
		client.logCall(ctx, "opensandbox", http.MethodPost, path, 0, startedAt, err)
		return err
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		requestErr := fmt.Errorf("%s lifecycle sandbox: %w", action, err)
		client.logCall(ctx, "opensandbox", http.MethodPost, path, 0, startedAt, requestErr)
		return requestErr
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusAccepted && response.StatusCode != http.StatusNoContent {
		requestErr := responseStatusError(action+" lifecycle sandbox", response)
		client.logCall(ctx, "opensandbox", http.MethodPost, path, response.StatusCode, startedAt, requestErr)
		return requestErr
	}
	client.logCall(
		ctx,
		"opensandbox",
		http.MethodPost,
		path,
		response.StatusCode,
		startedAt,
		nil,
		slog.String("sandbox_id", sandboxID),
		slog.String("action", action),
	)
	return nil
}

// DeleteSandbox deletes a sandbox through the lifecycle API when available,
// falling back to its discovered Kubernetes custom resources.
func (client *client) DeleteSandbox(ctx context.Context, sandbox Sandbox) error {
	if sandbox.ID == "" {
		return errors.New("sandbox ID is required")
	}

	var deleteErrors []error
	if contains(sandbox.Sources, SourceLifecycle) {
		deleted, err := client.deleteLifecycleSandbox(ctx, sandbox.ID)
		if err != nil {
			deleteErrors = append(deleteErrors, err)
		}
		if deleted {
			return nil
		}
	}

	deletedResource := false
	for _, resource := range sandbox.Resources {
		deleted, err := client.deleteCustomResource(ctx, resource)
		if err != nil {
			deleteErrors = append(deleteErrors, err)
			continue
		}
		deletedResource = deletedResource || deleted
	}
	if deletedResource {
		return nil
	}
	if len(deleteErrors) != 0 {
		return errors.Join(deleteErrors...)
	}
	return fmt.Errorf("sandbox %q has no deletable lifecycle or Kubernetes resource", sandbox.ID)
}

func (client *client) deleteLifecycleSandbox(ctx context.Context, sandboxID string) (bool, error) {
	path := "/sandboxes/" + url.PathEscape(sandboxID)
	startedAt := time.Now()
	request, err := client.newAPIRequest(ctx, http.MethodDelete, path, nil)
	if err != nil {
		client.logCall(ctx, "opensandbox", http.MethodDelete, path, 0, startedAt, err)
		return false, err
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		requestErr := fmt.Errorf("delete lifecycle sandbox: %w", err)
		client.logCall(ctx, "opensandbox", http.MethodDelete, path, 0, startedAt, requestErr)
		return false, requestErr
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		client.logCall(
			ctx,
			"opensandbox",
			http.MethodDelete,
			path,
			response.StatusCode,
			startedAt,
			nil,
			slog.String("sandbox_id", sandboxID),
		)
		return false, nil
	}
	if response.StatusCode != http.StatusNoContent {
		requestErr := responseStatusError("delete lifecycle sandbox", response)
		client.logCall(ctx, "opensandbox", http.MethodDelete, path, response.StatusCode, startedAt, requestErr)
		return false, requestErr
	}
	client.logCall(
		ctx,
		"opensandbox",
		http.MethodDelete,
		path,
		response.StatusCode,
		startedAt,
		nil,
		slog.String("sandbox_id", sandboxID),
	)
	return true, nil
}

func (client *client) deleteCustomResource(ctx context.Context, resource ResourceReference) (bool, error) {
	if resource.Group == "" || resource.Version == "" || resource.Plural == "" ||
		resource.Namespace == "" || resource.Name == "" {
		return false, errors.New("sandbox Kubernetes resource reference is incomplete")
	}
	path := fmt.Sprintf(
		"/apis/%s/%s/namespaces/%s/%s/%s",
		url.PathEscape(resource.Group),
		url.PathEscape(resource.Version),
		url.PathEscape(resource.Namespace),
		url.PathEscape(resource.Plural),
		url.PathEscape(resource.Name),
	)
	startedAt := time.Now()
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, client.proxyURL+path, nil)
	if err != nil {
		requestErr := fmt.Errorf("create Kubernetes sandbox delete request: %w", err)
		client.logCall(ctx, "kubernetes", http.MethodDelete, path, 0, startedAt, requestErr)
		return false, requestErr
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		requestErr := fmt.Errorf("delete Kubernetes sandbox resource: %w", err)
		client.logCall(ctx, "kubernetes", http.MethodDelete, path, 0, startedAt, requestErr)
		return false, requestErr
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		client.logCall(ctx, "kubernetes", http.MethodDelete, path, response.StatusCode, startedAt, nil)
		return false, nil
	}
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusAccepted {
		requestErr := responseStatusError("delete Kubernetes sandbox resource", response)
		client.logCall(ctx, "kubernetes", http.MethodDelete, path, response.StatusCode, startedAt, requestErr)
		return false, requestErr
	}
	client.logCall(
		ctx,
		"kubernetes",
		http.MethodDelete,
		path,
		response.StatusCode,
		startedAt,
		nil,
		slog.String("resource", resource.Name),
		slog.String("namespace", resource.Namespace),
	)
	return true, nil
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
