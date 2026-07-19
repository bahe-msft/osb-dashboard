package opensandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// CreateSandbox creates an image-backed sandbox.
func (client *client) CreateSandbox(ctx context.Context, request CreateSandboxRequest) (Sandbox, error) {
	if strings.TrimSpace(request.Image) == "" {
		return Sandbox{}, errors.New("sandbox image is required")
	}
	if len(request.Entrypoint) == 0 {
		return Sandbox{}, errors.New("sandbox entrypoint is required")
	}
	if request.Timeout <= 0 {
		return Sandbox{}, errors.New("sandbox timeout must be positive")
	}
	if len(request.ResourceLimits) == 0 {
		return Sandbox{}, errors.New("sandbox resource limits are required")
	}

	payload := apiCreateRequest{
		Image:          apiSandboxImage{URI: request.Image},
		Entrypoint:     request.Entrypoint,
		Timeout:        int(request.Timeout.Seconds()),
		ResourceLimits: request.ResourceLimits,
		Metadata:       request.Metadata,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Sandbox{}, fmt.Errorf("encode create sandbox request: %w", err)
	}

	httpRequest, err := client.newAPIRequest(ctx, http.MethodPost, "/sandboxes", bytes.NewReader(body))
	if err != nil {
		return Sandbox{}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")

	response, err := client.httpClient.Do(httpRequest)
	if err != nil {
		return Sandbox{}, fmt.Errorf("create sandbox: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		return Sandbox{}, responseStatusError("create sandbox", response)
	}

	var sandbox apiSandbox
	if err := json.NewDecoder(response.Body).Decode(&sandbox); err != nil {
		return Sandbox{}, fmt.Errorf("decode created sandbox: %w", err)
	}
	model := sandbox.model()
	model.Namespace = client.options.WorkloadNamespace
	return model, nil
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
	request, err := client.newAPIRequest(
		ctx,
		http.MethodDelete,
		"/sandboxes/"+url.PathEscape(sandboxID),
		nil,
	)
	if err != nil {
		return false, err
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		return false, fmt.Errorf("delete lifecycle sandbox: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if response.StatusCode != http.StatusNoContent {
		return false, responseStatusError("delete lifecycle sandbox", response)
	}
	return true, nil
}

func (client *client) deleteCustomResource(ctx context.Context, resource ResourceReference) (bool, error) {
	if resource.Group == "" || resource.Version == "" || resource.Plural == "" ||
		resource.Namespace == "" || resource.Name == "" {
		return false, errors.New("sandbox Kubernetes resource reference is incomplete")
	}
	endpoint := fmt.Sprintf(
		"%s/apis/%s/%s/namespaces/%s/%s/%s",
		client.proxyURL,
		url.PathEscape(resource.Group),
		url.PathEscape(resource.Version),
		url.PathEscape(resource.Namespace),
		url.PathEscape(resource.Plural),
		url.PathEscape(resource.Name),
	)
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return false, fmt.Errorf("create Kubernetes sandbox delete request: %w", err)
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		return false, fmt.Errorf("delete Kubernetes sandbox resource: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusAccepted {
		return false, responseStatusError("delete Kubernetes sandbox resource", response)
	}
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
