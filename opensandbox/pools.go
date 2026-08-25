package opensandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// ListPools returns OpenSandbox Pool custom resources from the workload namespace.
// A cluster without the Pool CRD is treated as having no pools.
func (client *client) ListPools(ctx context.Context) ([]Pool, error) {
	endpoint := fmt.Sprintf(
		"%s/apis/sandbox.opensandbox.io/v1alpha1/namespaces/%s/pools",
		client.proxyURL,
		url.PathEscape(client.options.WorkloadNamespace),
	)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create pool discovery request: %w", err)
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("list pool resources: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if response.StatusCode != http.StatusOK {
		return nil, responseStatusError("list pool resources", response)
	}

	var payload poolResourceList
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode pool resources: %w", err)
	}

	pools := make([]Pool, 0, len(payload.Items))
	for _, resource := range payload.Items {
		pools = append(pools, poolFromResource(resource))
	}
	sort.Slice(pools, func(i, j int) bool {
		if pools[i].CreatedAt.Equal(pools[j].CreatedAt) {
			return pools[i].Name < pools[j].Name
		}
		return pools[i].CreatedAt.After(pools[j].CreatedAt)
	})
	return pools, nil
}

// GetPool returns one OpenSandbox Pool custom resource by name.
func (client *client) GetPool(ctx context.Context, name string) (Pool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Pool{}, errors.New("pool name is required")
	}
	endpoint := fmt.Sprintf(
		"%s/apis/sandbox.opensandbox.io/v1alpha1/namespaces/%s/pools/%s",
		client.proxyURL,
		url.PathEscape(client.options.WorkloadNamespace),
		url.PathEscape(name),
	)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Pool{}, fmt.Errorf("create pool request: %w", err)
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		return Pool{}, fmt.Errorf("get pool resource: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Pool{}, responseStatusError("get pool resource", response)
	}

	var resource poolResource
	if err := json.NewDecoder(response.Body).Decode(&resource); err != nil {
		return Pool{}, fmt.Errorf("decode pool resource: %w", err)
	}
	return poolFromResource(resource), nil
}

func poolFromResource(resource poolResource) Pool {
	cpu, memory := firstContainerResources(resource.Spec.Template.Spec)
	return Pool{
		Name:         resource.Metadata.Name,
		Namespace:    resource.Metadata.Namespace,
		CreatedAt:    resource.Metadata.CreationTimestamp,
		Image:        firstContainerImage(resource.Spec.Template.Spec),
		CPU:          cpu,
		Memory:       memory,
		RuntimeClass: resource.Spec.Template.Spec.RuntimeClassName,
		BufferMin:    resource.Spec.CapacitySpec.BufferMin,
		BufferMax:    resource.Spec.CapacitySpec.BufferMax,
		PoolMin:      resource.Spec.CapacitySpec.PoolMin,
		PoolMax:      resource.Spec.CapacitySpec.PoolMax,
		Total:        resource.Status.Total,
		Allocated:    resource.Status.Allocated,
		Available:    resource.Status.Available,
	}
}
