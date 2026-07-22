package opensandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	kuberneteslabels "k8s.io/apimachinery/pkg/labels"
)

// ListSandboxes merges lifecycle API results with BatchSandbox and Agent
// Sandbox custom resources. CRD discovery is optional: a missing CRD is treated
// as an empty source, while authorization and transport failures are returned
// alongside any successfully discovered sandboxes.
func (client *client) ListSandboxes(ctx context.Context) ([]Sandbox, error) {
	startedAt := time.Now()
	pods, podsErr := client.listPods(ctx)
	lifecycleSandboxes, lifecycleErr := client.listLifecycleSandboxes(ctx)
	batchSandboxes, batchErr := client.listCustomResourceSandboxes(
		ctx,
		"sandbox.opensandbox.io",
		"v1alpha1",
		"batchsandboxes",
		SourceBatchSandbox,
		func(resource sandboxResource) Sandbox {
			return batchSandboxFromResource(resource, pods)
		},
	)
	agentSandboxes, agentErr := client.listCustomResourceSandboxes(
		ctx,
		"agents.x-k8s.io",
		"v1alpha1",
		"sandboxes",
		SourceAgentSandbox,
		func(resource sandboxResource) Sandbox {
			return agentSandboxFromResource(resource, pods)
		},
	)

	merged := mergeSandboxes(lifecycleSandboxes, batchSandboxes, agentSandboxes)
	discoveryErr := errors.Join(lifecycleErr, batchErr, agentErr, podsErr)
	level := slog.LevelInfo
	if discoveryErr != nil {
		level = slog.LevelWarn
	}
	client.logger.LogAttrs(
		ctx,
		level,
		"sandbox discovery",
		slog.Int("lifecycle_count", len(lifecycleSandboxes)),
		slog.Int("batchsandbox_count", len(batchSandboxes)),
		slog.Int("agentsandbox_count", len(agentSandboxes)),
		slog.Int("merged_count", len(merged)),
		slog.Duration("duration", time.Since(startedAt)),
		slog.Any("error", discoveryErr),
	)
	return merged, discoveryErr
}

func (client *client) listLifecycleSandboxes(ctx context.Context) ([]Sandbox, error) {
	const path = "/sandboxes?page=1&pageSize=100"
	startedAt := time.Now()
	request, err := client.newAPIRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		client.logCall(ctx, "opensandbox", http.MethodGet, path, 0, startedAt, err)
		return nil, err
	}

	response, err := client.httpClient.Do(request)
	if err != nil {
		requestErr := fmt.Errorf("list lifecycle sandboxes: %w", err)
		client.logCall(ctx, "opensandbox", http.MethodGet, path, 0, startedAt, requestErr)
		return nil, requestErr
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		requestErr := responseStatusError("list lifecycle sandboxes", response)
		client.logCall(ctx, "opensandbox", http.MethodGet, path, response.StatusCode, startedAt, requestErr)
		return nil, requestErr
	}

	var payload listResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		decodeErr := fmt.Errorf("decode lifecycle sandbox list: %w", err)
		client.logCall(ctx, "opensandbox", http.MethodGet, path, response.StatusCode, startedAt, decodeErr)
		return nil, decodeErr
	}

	sandboxes := make([]Sandbox, 0, len(payload.Items))
	for _, sandbox := range payload.Items {
		model := sandbox.model()
		model.Namespace = client.options.WorkloadNamespace
		sandboxes = append(sandboxes, model)
	}
	client.logCall(
		ctx,
		"opensandbox",
		http.MethodGet,
		path,
		response.StatusCode,
		startedAt,
		nil,
		slog.Int("sandbox_count", len(sandboxes)),
	)
	return sandboxes, nil
}

func (client *client) listPods(ctx context.Context) ([]podResource, error) {
	endpoint := fmt.Sprintf(
		"%s/api/v1/namespaces/%s/pods",
		client.proxyURL,
		url.PathEscape(client.options.WorkloadNamespace),
	)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create pod discovery request: %w", err)
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("list sandbox pods: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, responseStatusError("list sandbox pods", response)
	}

	var payload podList
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode sandbox pods: %w", err)
	}
	return payload.Items, nil
}

func (client *client) listCustomResourceSandboxes(
	ctx context.Context,
	group string,
	version string,
	plural string,
	source string,
	convert func(sandboxResource) Sandbox,
) ([]Sandbox, error) {
	endpoint := fmt.Sprintf(
		"%s/apis/%s/%s/namespaces/%s/%s",
		client.proxyURL,
		url.PathEscape(group),
		url.PathEscape(version),
		url.PathEscape(client.options.WorkloadNamespace),
		url.PathEscape(plural),
	)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create %s discovery request: %w", source, err)
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("list %s resources: %w", source, err)
	}
	defer response.Body.Close()

	if response.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if response.StatusCode != http.StatusOK {
		return nil, responseStatusError("list "+source+" resources", response)
	}

	var payload customResourceList
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode %s resources: %w", source, err)
	}

	sandboxes := make([]Sandbox, 0, len(payload.Items))
	for _, resource := range payload.Items {
		sandbox := convert(resource)
		if sandbox.ID == "" {
			continue
		}
		sandboxes = append(sandboxes, sandbox)
	}
	return sandboxes, nil
}

func batchSandboxFromResource(resource sandboxResource, pods []podResource) Sandbox {
	cpu, memory := firstContainerResources(resource.Spec.Template.Spec)
	return Sandbox{
		ID:        resourceID(resource.Metadata),
		State:     batchSandboxState(resource.Status),
		CreatedAt: resource.Metadata.CreationTimestamp,
		Namespace: resource.Metadata.Namespace,
		PodName:   batchSandboxPodName(resource, pods),
		Image:     firstContainerImage(resource.Spec.Template.Spec),
		CPU:       cpu,
		Memory:    memory,
		Metadata:  userMetadata(resource.Metadata.Labels),
		Sources:   []string{SourceBatchSandbox},
		Resources: []ResourceReference{{
			Source:    SourceBatchSandbox,
			Group:     "sandbox.opensandbox.io",
			Version:   "v1alpha1",
			Plural:    "batchsandboxes",
			Namespace: resource.Metadata.Namespace,
			Name:      resource.Metadata.Name,
		}},
	}
}

func agentSandboxFromResource(resource sandboxResource, pods []podResource) Sandbox {
	cpu, memory := firstContainerResources(resource.Spec.PodTemplate.Spec)
	return Sandbox{
		ID:        resourceID(resource.Metadata),
		State:     agentSandboxState(resource.Status),
		CreatedAt: resource.Metadata.CreationTimestamp,
		Namespace: resource.Metadata.Namespace,
		PodName:   agentSandboxPodName(resource, pods),
		Image:     firstContainerImage(resource.Spec.PodTemplate.Spec),
		CPU:       cpu,
		Memory:    memory,
		Metadata:  userMetadata(resource.Metadata.Labels),
		Sources:   []string{SourceAgentSandbox},
		Resources: []ResourceReference{{
			Source:    SourceAgentSandbox,
			Group:     "agents.x-k8s.io",
			Version:   "v1alpha1",
			Plural:    "sandboxes",
			Namespace: resource.Metadata.Namespace,
			Name:      resource.Metadata.Name,
		}},
	}
}

func batchSandboxPodName(resource sandboxResource, pods []podResource) string {
	for _, pod := range sortedPods(pods) {
		if pod.Metadata.Labels["batch-sandbox.sandbox.opensandbox.io/name"] == resource.Metadata.Name ||
			ownedBy(pod, "BatchSandbox", resource.Metadata.Name) {
			return pod.Metadata.Name
		}
	}
	return ""
}

func agentSandboxPodName(resource sandboxResource, pods []podResource) string {
	if resource.Status.Selector != "" {
		selector, err := kuberneteslabels.Parse(resource.Status.Selector)
		if err == nil {
			for _, pod := range sortedPods(pods) {
				if selector.Matches(kuberneteslabels.Set(pod.Metadata.Labels)) {
					return pod.Metadata.Name
				}
			}
		}
	}
	for _, pod := range sortedPods(pods) {
		if ownedBy(pod, "Sandbox", resource.Metadata.Name) {
			return pod.Metadata.Name
		}
	}
	return ""
}

func sortedPods(pods []podResource) []podResource {
	result := append([]podResource(nil), pods...)
	sort.Slice(result, func(i, j int) bool {
		return result[i].Metadata.Name < result[j].Metadata.Name
	})
	return result
}

func ownedBy(pod podResource, kind, name string) bool {
	for _, owner := range pod.Metadata.OwnerReferences {
		if owner.Kind == kind && owner.Name == name {
			return true
		}
	}
	return false
}

func resourceID(metadata resourceMetadata) string {
	if sandboxID := metadata.Labels["opensandbox.io/id"]; sandboxID != "" {
		return sandboxID
	}
	return metadata.Name
}

func batchSandboxState(status resourceStatus) string {
	switch strings.ToLower(status.Phase) {
	case "succeed", "running":
		return "Running"
	case "pending":
		return "Pending"
	case "pausing":
		return "Pausing"
	case "paused":
		return "Paused"
	case "resuming":
		return "Resuming"
	case "failed":
		return "Failed"
	}
	if status.Ready > 0 || conditionIsTrue(status.Conditions, "Ready") {
		return "Running"
	}
	if conditionIsTrue(status.Conditions, "PodFailed") ||
		conditionIsTrue(status.Conditions, "PauseFailed") ||
		conditionIsTrue(status.Conditions, "ResumeFailed") {
		return "Failed"
	}
	return "Pending"
}

func agentSandboxState(status resourceStatus) string {
	for _, condition := range status.Conditions {
		if condition.Type != "Ready" {
			continue
		}
		if condition.Status == "True" {
			return "Running"
		}
		if condition.Reason == "SandboxExpired" {
			return "Terminated"
		}
		return "Pending"
	}
	return "Pending"
}

func conditionIsTrue(conditions []resourceCondition, conditionType string) bool {
	for _, condition := range conditions {
		if condition.Type == conditionType && condition.Status == "True" {
			return true
		}
	}
	return false
}

func firstContainerImage(spec podSpec) string {
	if len(spec.Containers) == 0 {
		return ""
	}
	return spec.Containers[0].Image
}

func firstContainerResources(spec podSpec) (string, string) {
	if len(spec.Containers) == 0 {
		return "", ""
	}
	resources := spec.Containers[0].Resources
	cpu := resources.Requests["cpu"]
	memory := resources.Requests["memory"]
	if cpu == "" {
		cpu = resources.Limits["cpu"]
	}
	if memory == "" {
		memory = resources.Limits["memory"]
	}
	return cpu, memory
}

func userMetadata(labels map[string]string) map[string]string {
	metadata := make(map[string]string)
	for key, value := range labels {
		if strings.HasPrefix(key, "opensandbox.io/") ||
			strings.HasPrefix(key, "batch-sandbox.sandbox.opensandbox.io/") {
			continue
		}
		metadata[key] = value
	}
	if len(metadata) == 0 {
		return nil
	}
	return metadata
}

func mergeSandboxes(sources ...[]Sandbox) []Sandbox {
	byID := make(map[string]Sandbox)
	for _, source := range sources {
		for _, sandbox := range source {
			if sandbox.ID == "" {
				continue
			}
			existing, exists := byID[sandbox.ID]
			if !exists {
				byID[sandbox.ID] = sandbox
				continue
			}
			existing.Sources = appendUnique(existing.Sources, sandbox.Sources...)
			existing.Resources = appendUniqueResources(existing.Resources, sandbox.Resources...)
			if existing.State == "" {
				existing.State = sandbox.State
			}
			if existing.CreatedAt.IsZero() {
				existing.CreatedAt = sandbox.CreatedAt
			}
			if existing.Namespace == "" {
				existing.Namespace = sandbox.Namespace
			}
			if existing.PodName == "" {
				existing.PodName = sandbox.PodName
			}
			if existing.Image == "" {
				existing.Image = sandbox.Image
			}
			if existing.CPU == "" {
				existing.CPU = sandbox.CPU
			}
			if existing.Memory == "" {
				existing.Memory = sandbox.Memory
			}
			if len(existing.Metadata) == 0 {
				existing.Metadata = sandbox.Metadata
			}
			byID[sandbox.ID] = existing
		}
	}

	result := make([]Sandbox, 0, len(byID))
	for _, sandbox := range byID {
		result = append(result, sandbox)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].CreatedAt.Equal(result[j].CreatedAt) {
			return result[i].ID < result[j].ID
		}
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})
	return result
}

func appendUniqueResources(values []ResourceReference, additions ...ResourceReference) []ResourceReference {
	seen := make(map[string]bool, len(values)+len(additions))
	for _, value := range values {
		seen[value.Source+"/"+value.Namespace+"/"+value.Name] = true
	}
	for _, value := range additions {
		key := value.Source + "/" + value.Namespace + "/" + value.Name
		if value.Name == "" || seen[key] {
			continue
		}
		values = append(values, value)
		seen[key] = true
	}
	return values
}

func appendUnique(values []string, additions ...string) []string {
	seen := make(map[string]bool, len(values)+len(additions))
	for _, value := range values {
		seen[value] = true
	}
	for _, value := range additions {
		if value == "" || seen[value] {
			continue
		}
		values = append(values, value)
		seen[value] = true
	}
	return values
}
