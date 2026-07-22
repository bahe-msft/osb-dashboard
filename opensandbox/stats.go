package opensandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
)

// ListSandboxNodeLoads returns requested sandbox resources grouped by the nodes
// currently hosting sandbox pods. It uses only the core Kubernetes API and does
// not require Metrics Server.
func (client *client) ListSandboxNodeLoads(ctx context.Context) ([]SandboxNodeLoad, error) {
	pods, podsErr := client.listPods(ctx)
	nodes, nodesErr := client.listNodes(ctx)

	loads := make(map[string]*SandboxNodeLoad)
	for _, pod := range pods {
		if !isScheduledSandboxPod(pod) {
			continue
		}
		load := loads[pod.Spec.NodeName]
		if load == nil {
			load = &SandboxNodeLoad{Name: pod.Spec.NodeName}
			loads[pod.Spec.NodeName] = load
		}
		load.SandboxCount++
		for _, container := range pod.Spec.Containers {
			cpu, memory := requestedResources(container.Resources)
			load.CPURequestedMilli += cpu
			load.MemoryRequestedBytes += memory
		}
	}

	for _, node := range nodes {
		load := loads[node.Metadata.Name]
		if load == nil {
			continue
		}
		load.CPUAllocatableMilli = quantityMilli(node.Status.Allocatable["cpu"])
		load.MemoryAllocatableBytes = quantityValue(node.Status.Allocatable["memory"])
	}

	result := make([]SandboxNodeLoad, 0, len(loads))
	for _, load := range loads {
		result = append(result, *load)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, errors.Join(podsErr, nodesErr)
}

func (client *client) listNodes(ctx context.Context) ([]nodeResource, error) {
	endpoint := client.proxyURL + "/api/v1/nodes"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create node discovery request: %w", err)
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("list Kubernetes nodes: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, responseStatusError("list Kubernetes nodes", response)
	}
	var payload nodeList
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode Kubernetes nodes: %w", err)
	}
	return payload.Items, nil
}

func isScheduledSandboxPod(pod podResource) bool {
	if pod.Spec.NodeName == "" {
		return false
	}
	phase := strings.ToLower(strings.TrimSpace(pod.Status.Phase))
	if phase == "succeeded" || phase == "failed" {
		return false
	}
	if pod.Metadata.Labels["opensandbox.io/id"] != "" ||
		pod.Metadata.Labels["batch-sandbox.sandbox.opensandbox.io/name"] != "" {
		return true
	}
	for _, owner := range pod.Metadata.OwnerReferences {
		if owner.Kind == "BatchSandbox" || owner.Kind == "Sandbox" {
			return true
		}
	}
	return false
}

func requestedResources(resources containerResources) (int64, int64) {
	cpu := resources.Requests["cpu"]
	memory := resources.Requests["memory"]
	if cpu == "" {
		cpu = resources.Limits["cpu"]
	}
	if memory == "" {
		memory = resources.Limits["memory"]
	}
	return quantityMilli(cpu), quantityValue(memory)
}

func quantityMilli(value string) int64 {
	quantity, err := resource.ParseQuantity(value)
	if err != nil {
		return 0
	}
	return quantity.MilliValue()
}

func quantityValue(value string) int64 {
	quantity, err := resource.ParseQuantity(value)
	if err != nil {
		return 0
	}
	return quantity.Value()
}
