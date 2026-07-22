package opensandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// ListPodEvents returns Kubernetes events for a sandbox pod, aggregating
// repeated type/reason/message entries across event objects.
func (client *client) ListPodEvents(ctx context.Context, podName string) ([]SandboxEvent, error) {
	podName = strings.TrimSpace(podName)
	if podName == "" {
		return nil, nil
	}
	events, err := client.listKubernetesEvents(ctx, "involvedObject.kind=Pod,involvedObject.name="+podName)
	if err != nil {
		return nil, err
	}
	result := aggregatePodEvents(events)
	for index := range result {
		result[index].PodName = podName
	}
	return result, nil
}

// ListRecentSandboxEvents returns aggregated events for all currently known
// sandbox pods using one Kubernetes events request.
func (client *client) ListRecentSandboxEvents(ctx context.Context, sandboxes []Sandbox) ([]SandboxEvent, error) {
	byPod := make(map[string]string)
	for _, sandbox := range sandboxes {
		if sandbox.PodName != "" {
			byPod[sandbox.PodName] = sandbox.ID
		}
	}
	if len(byPod) == 0 {
		return nil, nil
	}
	events, err := client.listKubernetesEvents(ctx, "involvedObject.kind=Pod")
	if err != nil {
		return nil, err
	}
	grouped := make(map[string][]kubernetesEvent)
	for _, event := range events {
		if _, exists := byPod[event.InvolvedObject.Name]; exists {
			grouped[event.InvolvedObject.Name] = append(grouped[event.InvolvedObject.Name], event)
		}
	}
	var result []SandboxEvent
	for podName, podEvents := range grouped {
		for _, event := range aggregatePodEvents(podEvents) {
			event.PodName = podName
			event.SandboxID = byPod[podName]
			result = append(result, event)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].LastSeen.After(result[j].LastSeen) })
	return result, nil
}

func (client *client) listKubernetesEvents(ctx context.Context, selector string) ([]kubernetesEvent, error) {
	endpoint := fmt.Sprintf(
		"%s/api/v1/namespaces/%s/events?fieldSelector=%s",
		client.proxyURL,
		url.PathEscape(client.options.WorkloadNamespace),
		url.QueryEscape(selector),
	)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create pod events request: %w", err)
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("list pod events: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, responseStatusError("list pod events", response)
	}
	var payload kubernetesEventList
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode pod events: %w", err)
	}
	return payload.Items, nil
}

func aggregatePodEvents(events []kubernetesEvent) []SandboxEvent {
	aggregated := make(map[string]SandboxEvent)
	for _, event := range events {
		if strings.TrimSpace(event.Reason) == "" && strings.TrimSpace(event.Message) == "" {
			continue
		}
		count := event.Count
		if count < 1 {
			count = 1
		}
		firstSeen := firstEventTime(event)
		lastSeen := lastEventTime(event)
		key := event.Type + "\x00" + event.Reason + "\x00" + event.Message
		current, exists := aggregated[key]
		if !exists {
			aggregated[key] = SandboxEvent{
				Type:      event.Type,
				Reason:    event.Reason,
				Message:   event.Message,
				Source:    eventSource(event),
				Count:     count,
				FirstSeen: firstSeen,
				LastSeen:  lastSeen,
			}
			continue
		}
		current.Count += count
		if current.FirstSeen.IsZero() || (!firstSeen.IsZero() && firstSeen.Before(current.FirstSeen)) {
			current.FirstSeen = firstSeen
		}
		if lastSeen.After(current.LastSeen) {
			current.LastSeen = lastSeen
			current.Source = eventSource(event)
		}
		aggregated[key] = current
	}

	result := make([]SandboxEvent, 0, len(aggregated))
	for _, event := range aggregated {
		result = append(result, event)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].LastSeen.Equal(result[j].LastSeen) {
			return result[i].Reason < result[j].Reason
		}
		return result[i].LastSeen.After(result[j].LastSeen)
	})
	return result
}

func firstEventTime(event kubernetesEvent) time.Time {
	return firstNonZeroTime(event.FirstTimestamp, event.EventTime, event.Metadata.CreationTimestamp)
}

func lastEventTime(event kubernetesEvent) time.Time {
	return firstNonZeroTime(event.Series.LastObservedTime, event.LastTimestamp, event.EventTime, event.Metadata.CreationTimestamp)
}

func firstNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}

func eventSource(event kubernetesEvent) string {
	if event.ReportingController != "" {
		return event.ReportingController
	}
	return event.Source.Component
}

type kubernetesEventList struct {
	Items []kubernetesEvent `json:"items"`
}

type kubernetesEvent struct {
	Metadata            kubernetesEventMetadata `json:"metadata"`
	Type                string                  `json:"type"`
	Reason              string                  `json:"reason"`
	Message             string                  `json:"message"`
	Count               int32                   `json:"count"`
	FirstTimestamp      time.Time               `json:"firstTimestamp"`
	LastTimestamp       time.Time               `json:"lastTimestamp"`
	EventTime           time.Time               `json:"eventTime"`
	Series              kubernetesEventSeries   `json:"series"`
	ReportingController string                  `json:"reportingController"`
	Source              kubernetesEventSource   `json:"source"`
	InvolvedObject      kubernetesObjectRef     `json:"involvedObject"`
}

type kubernetesEventMetadata struct {
	CreationTimestamp time.Time `json:"creationTimestamp"`
}

type kubernetesEventSeries struct {
	LastObservedTime time.Time `json:"lastObservedTime"`
}

type kubernetesEventSource struct {
	Component string `json:"component"`
}

type kubernetesObjectRef struct {
	Name string `json:"name"`
}
