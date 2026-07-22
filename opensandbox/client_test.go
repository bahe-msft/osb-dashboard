package opensandbox

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestReadAndWriteOperations(t *testing.T) {
	var secretRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/namespaces/test/secrets/api-key":
			secretRequests.Add(1)
			_ = json.NewEncoder(w).Encode(kubeSecret{Data: map[string]string{
				"token": base64.StdEncoding.EncodeToString([]byte("test-key")),
			}})
		case r.URL.Path == "/api/v1/namespaces/opensandbox/pods":
			_ = json.NewEncoder(w).Encode(podList{})
		case r.URL.Path == "/api/v1/namespaces/test/services/http:lifecycle:http/proxy/sandboxes" && r.Method == http.MethodGet:
			assertAPIKey(t, r)
			_ = json.NewEncoder(w).Encode(listResponse{Items: []apiSandbox{{
				ID:        "sandbox-1",
				Status:    apiSandboxStatus{State: "Running"},
				CreatedAt: time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC),
				Image:     apiSandboxImage{URI: "python:3.12-slim"},
			}}})
		case r.URL.Path == "/api/v1/namespaces/test/services/http:lifecycle:http/proxy/sandboxes" && r.Method == http.MethodPost:
			assertAPIKey(t, r)
			var request apiCreateRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode create request: %v", err)
			}
			if request.Image == nil || request.Image.URI != "python:3.12-slim" {
				t.Errorf("image = %#v", request.Image)
			}
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(apiSandbox{
				ID:        "sandbox-2",
				Status:    apiSandboxStatus{State: "Pending"},
				CreatedAt: time.Date(2026, time.July, 19, 12, 1, 0, 0, time.UTC),
			})
		case r.URL.Path == "/api/v1/namespaces/test/services/http:lifecycle:http/proxy/sandboxes/sandbox-2/pause" && r.Method == http.MethodPost:
			assertAPIKey(t, r)
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/api/v1/namespaces/test/services/http:lifecycle:http/proxy/sandboxes/sandbox-2/resume" && r.Method == http.MethodPost:
			assertAPIKey(t, r)
			w.WriteHeader(http.StatusAccepted)
		case r.URL.Path == "/api/v1/namespaces/test/services/http:lifecycle:http/proxy/sandboxes/sandbox-2" && r.Method == http.MethodDelete:
			assertAPIKey(t, r)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var logOutput bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logOutput, nil))
	combined, err := newClient(server.URL, server.Client(), Options{
		Namespace:        "test",
		ServiceName:      "lifecycle",
		ServicePort:      "http",
		APIKeySecretName: "api-key",
		APIKeySecretKey:  "token",
		Logger:           logger,
	}, nil)
	if err != nil {
		t.Fatalf("newClient() error = %v", err)
	}
	var reader Reader = combined
	var writer Writer = combined

	sandboxes, err := reader.ListSandboxes(context.Background())
	if err != nil {
		t.Fatalf("ListSandboxes() error = %v", err)
	}
	if len(sandboxes) != 1 || sandboxes[0].ID != "sandbox-1" || sandboxes[0].State != "Running" {
		t.Fatalf("ListSandboxes() = %#v", sandboxes)
	}

	created, err := writer.CreateSandbox(context.Background(), CreateSandboxRequest{
		Image:          "python:3.12-slim",
		Entrypoint:     []string{"tail", "-f", "/dev/null"},
		Timeout:        10 * time.Minute,
		ResourceLimits: map[string]string{"cpu": "250m", "memory": "256Mi"},
	})
	if err != nil {
		t.Fatalf("CreateSandbox() error = %v", err)
	}
	if created.ID != "sandbox-2" || created.State != "Pending" {
		t.Fatalf("CreateSandbox() = %#v", created)
	}
	if err := writer.PauseSandbox(context.Background(), created.ID); err != nil {
		t.Fatalf("PauseSandbox() error = %v", err)
	}
	if err := writer.ResumeSandbox(context.Background(), created.ID); err != nil {
		t.Fatalf("ResumeSandbox() error = %v", err)
	}
	if err := writer.DeleteSandbox(context.Background(), created); err != nil {
		t.Fatalf("DeleteSandbox() error = %v", err)
	}
	if got := secretRequests.Load(); got != 1 {
		t.Errorf("API key secret requests = %d, want 1", got)
	}
	for _, expected := range []string{"system=opensandbox", "method=GET", "method=POST", "method=DELETE", "sandbox_id=sandbox-2"} {
		if !strings.Contains(logOutput.String(), expected) {
			t.Errorf("log output does not contain %q: %s", expected, logOutput.String())
		}
	}
}

func TestListSandboxesMergesLifecycleAndCustomResources(t *testing.T) {
	createdAt := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/namespaces/control/secrets/api-key":
			_ = json.NewEncoder(w).Encode(kubeSecret{Data: map[string]string{
				"token": base64.StdEncoding.EncodeToString([]byte("test-key")),
			}})
		case "/api/v1/namespaces/workloads/pods":
			_ = json.NewEncoder(w).Encode(podList{Items: []podResource{
				{Metadata: podMetadata{Name: "batch-direct-0", Labels: map[string]string{"batch-sandbox.sandbox.opensandbox.io/name": "batch-direct"}}},
				{Metadata: podMetadata{Name: "agent-direct-pod", Labels: map[string]string{"agent": "agent-direct"}}},
			}})
		case "/api/v1/namespaces/control/services/http:lifecycle:http/proxy/sandboxes":
			_ = json.NewEncoder(w).Encode(listResponse{Items: []apiSandbox{{
				ID:        "shared",
				Status:    apiSandboxStatus{State: "Running"},
				CreatedAt: createdAt,
				Image:     apiSandboxImage{URI: "lifecycle:image"},
			}}})
		case "/apis/sandbox.opensandbox.io/v1alpha1/namespaces/workloads/batchsandboxes":
			_ = json.NewEncoder(w).Encode(customResourceList{Items: []sandboxResource{
				{
					Metadata: resourceMetadata{
						Name:              "batch-shared",
						Labels:            map[string]string{"opensandbox.io/id": "shared"},
						CreationTimestamp: createdAt,
					},
					Status: resourceStatus{Phase: "Paused"},
				},
				{
					Metadata: resourceMetadata{
						Name:              "batch-direct",
						Namespace:         "workloads",
						Labels:            map[string]string{"team": "dashboard"},
						CreationTimestamp: createdAt.Add(time.Minute),
					},
					Spec: resourceSpec{Template: podTemplate{Spec: podSpec{Containers: []containerSpec{{
						Image: "batch:image",
						Resources: containerResources{Requests: map[string]string{
							"cpu": "2", "memory": "4Gi",
						}},
					}}}}},
					Status: resourceStatus{Phase: "Succeed"},
				},
			}})
		case "/apis/agents.x-k8s.io/v1alpha1/namespaces/workloads/sandboxes":
			_ = json.NewEncoder(w).Encode(customResourceList{Items: []sandboxResource{{
				Metadata: resourceMetadata{
					Name:              "agent-direct",
					Namespace:         "workloads",
					CreationTimestamp: createdAt.Add(2 * time.Minute),
				},
				Spec: resourceSpec{PodTemplate: podTemplate{Spec: podSpec{Containers: []containerSpec{{
					Image: "agent:image",
					Resources: containerResources{Limits: map[string]string{
						"cpu": "4", "memory": "8Gi",
					}},
				}}}}},
				Status: resourceStatus{Selector: "agent=agent-direct", Conditions: []resourceCondition{{Type: "Ready", Status: "True"}}},
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	combined, err := newClient(server.URL, server.Client(), Options{
		Namespace:         "control",
		WorkloadNamespace: "workloads",
		ServiceName:       "lifecycle",
		ServicePort:       "http",
		APIKeySecretName:  "api-key",
		APIKeySecretKey:   "token",
	}, nil)
	if err != nil {
		t.Fatalf("newClient() error = %v", err)
	}

	sandboxes, err := combined.ListSandboxes(context.Background())
	if err != nil {
		t.Fatalf("ListSandboxes() error = %v", err)
	}
	if len(sandboxes) != 3 {
		t.Fatalf("len(sandboxes) = %d, want 3: %#v", len(sandboxes), sandboxes)
	}

	byID := make(map[string]Sandbox, len(sandboxes))
	for _, sandbox := range sandboxes {
		byID[sandbox.ID] = sandbox
	}
	if got := byID["shared"]; got.State != "Running" || got.Image != "lifecycle:image" || strings.Join(got.Sources, ",") != "lifecycle,batchsandbox" {
		t.Errorf("shared sandbox = %#v", got)
	}
	if got := byID["batch-direct"]; got.State != "Running" || got.Namespace != "workloads" || got.PodName != "batch-direct-0" || got.Image != "batch:image" || got.CPU != "2" || got.Memory != "4Gi" || got.Metadata["team"] != "dashboard" {
		t.Errorf("BatchSandbox = %#v", got)
	}
	if got := byID["agent-direct"]; got.State != "Running" || got.Namespace != "workloads" || got.PodName != "agent-direct-pod" || got.Image != "agent:image" || got.CPU != "4" || got.Memory != "8Gi" || strings.Join(got.Sources, ",") != "agentsandbox" {
		t.Errorf("Agent Sandbox = %#v", got)
	}
}

func TestListSandboxesReturnsCRDResultsWithLifecycleError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/namespaces/control/secrets/api-key":
			_ = json.NewEncoder(w).Encode(kubeSecret{Data: map[string]string{
				"token": base64.StdEncoding.EncodeToString([]byte("test-key")),
			}})
		case "/api/v1/namespaces/workloads/pods":
			_ = json.NewEncoder(w).Encode(podList{})
		case "/api/v1/namespaces/control/services/http:lifecycle:http/proxy/sandboxes":
			http.Error(w, "lifecycle unavailable", http.StatusServiceUnavailable)
		case "/apis/sandbox.opensandbox.io/v1alpha1/namespaces/workloads/batchsandboxes":
			_ = json.NewEncoder(w).Encode(customResourceList{Items: []sandboxResource{{
				Metadata: resourceMetadata{Name: "batch-direct"},
				Status:   resourceStatus{Phase: "Succeed"},
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	combined, err := newClient(server.URL, server.Client(), Options{
		Namespace:         "control",
		WorkloadNamespace: "workloads",
		ServiceName:       "lifecycle",
		ServicePort:       "http",
		APIKeySecretName:  "api-key",
		APIKeySecretKey:   "token",
	}, nil)
	if err != nil {
		t.Fatalf("newClient() error = %v", err)
	}

	sandboxes, err := combined.ListSandboxes(context.Background())
	if err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("ListSandboxes() error = %v, want HTTP 503", err)
	}
	if len(sandboxes) != 1 || sandboxes[0].ID != "batch-direct" {
		t.Fatalf("ListSandboxes() = %#v", sandboxes)
	}
}

func TestDirectLifecycleEndpointKeepsKubernetesAndLifecycleTransportsSeparate(t *testing.T) {
	var lifecycleRequests atomic.Int32
	lifecycleServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lifecycleRequests.Add(1)
		if r.URL.Path != "/sandboxes" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "" {
			t.Errorf("lifecycle request received Kubernetes Authorization header")
		}
		assertAPIKey(t, r)
		_ = json.NewEncoder(w).Encode(listResponse{})
	}))
	defer lifecycleServer.Close()

	kubeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/namespaces/control/secrets/api-key":
			_ = json.NewEncoder(w).Encode(kubeSecret{Data: map[string]string{
				"token": base64.StdEncoding.EncodeToString([]byte("test-key")),
			}})
		case "/api/v1/namespaces/workloads/pods":
			_ = json.NewEncoder(w).Encode(podList{})
		case "/apis/sandbox.opensandbox.io/v1alpha1/namespaces/workloads/batchsandboxes",
			"/apis/agents.x-k8s.io/v1alpha1/namespaces/workloads/sandboxes":
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	defer kubeServer.Close()

	kubeHTTPClient := kubeServer.Client()
	kubeTransport := kubeHTTPClient.Transport
	kubeHTTPClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		request = request.Clone(request.Context())
		request.Header.Set("Authorization", "Bearer kube-token")
		return kubeTransport.RoundTrip(request)
	})
	client, err := newClient(kubeServer.URL, kubeHTTPClient, Options{
		Namespace:         "control",
		WorkloadNamespace: "workloads",
		APIKeySecretName:  "api-key",
		APIKeySecretKey:   "token",
		LifecycleEndpoint: lifecycleServer.URL,
	}, nil)
	if err != nil {
		t.Fatalf("newClient() error = %v", err)
	}
	if _, err := client.ListSandboxes(context.Background()); err != nil {
		t.Fatalf("ListSandboxes() error = %v", err)
	}
	if lifecycleRequests.Load() != 1 {
		t.Fatalf("lifecycle requests = %d, want 1", lifecycleRequests.Load())
	}
}

func TestDirectLifecycleEndpointValidation(t *testing.T) {
	for _, endpoint := range []string{"ftp://example.test", "http:///missing-host", "https://example.test?query=value"} {
		_, err := newClient("http://localhost", http.DefaultClient, Options{LifecycleEndpoint: endpoint}, nil)
		if err == nil {
			t.Errorf("LifecycleEndpoint %q unexpectedly succeeded", endpoint)
		}
	}
	_, err := newClient("http://localhost", http.DefaultClient, Options{LifecycleHTTPClient: http.DefaultClient}, nil)
	if err == nil {
		t.Error("LifecycleHTTPClient without LifecycleEndpoint unexpectedly succeeded")
	}
}

func TestNewFromKubeconfig(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/namespaces/opensandbox/pods" {
			_ = json.NewEncoder(w).Encode(podList{})
			return
		}
		if r.URL.Path == "/api/v1/namespaces/opensandbox-system/secrets/opensandbox-api-key" {
			_ = json.NewEncoder(w).Encode(kubeSecret{Data: map[string]string{
				"api-key": base64.StdEncoding.EncodeToString([]byte("test-key")),
			}})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/proxy/sandboxes") {
			_ = json.NewEncoder(w).Encode(listResponse{})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	kubeconfigPath := filepath.Join(t.TempDir(), "config")
	kubeconfig := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: test
  cluster:
    server: %s
contexts:
- name: test
  context:
    cluster: test
    user: test
current-context: test
users:
- name: test
  user:
    token: kube-token
`, server.URL)
	if err := os.WriteFile(kubeconfigPath, []byte(kubeconfig), 0o600); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}

	client, err := NewFromKubeconfig(kubeconfigPath, Options{})
	if err != nil {
		t.Fatalf("NewFromKubeconfig() error = %v", err)
	}
	defer client.Close()
	if _, err := client.ListSandboxes(context.Background()); err != nil {
		t.Fatalf("ListSandboxes() error = %v", err)
	}
}

func TestListPodEventsAggregatesRepeatedEvents(t *testing.T) {
	first := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	last := first.Add(2 * time.Minute)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/namespaces/workloads/events" || !strings.Contains(r.URL.Query().Get("fieldSelector"), "involvedObject.name=sandbox-1-0") {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(kubernetesEventList{Items: []kubernetesEvent{
			{Type: "Warning", Reason: "FailedScheduling", Message: "Insufficient cpu", Count: 0, FirstTimestamp: first, LastTimestamp: first, Source: kubernetesEventSource{Component: "scheduler"}},
			{Type: "Warning", Reason: "FailedScheduling", Message: "Insufficient cpu", Count: 2, FirstTimestamp: first.Add(time.Minute), LastTimestamp: last, ReportingController: "default-scheduler"},
			{Type: "Normal", Reason: "Scheduled", Message: "Assigned to node-a", Count: 1, LastTimestamp: first.Add(90 * time.Second)},
		}})
	}))
	defer server.Close()

	client, err := newClient(server.URL, server.Client(), Options{WorkloadNamespace: "workloads"}, nil)
	if err != nil {
		t.Fatalf("newClient() error = %v", err)
	}
	events, err := client.ListPodEvents(context.Background(), "sandbox-1-0")
	if err != nil {
		t.Fatalf("ListPodEvents() error = %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("ListPodEvents() = %#v", events)
	}
	if events[0].Reason != "FailedScheduling" || events[0].Count != 3 || !events[0].FirstSeen.Equal(first) || !events[0].LastSeen.Equal(last) || events[0].Source != "default-scheduler" {
		t.Errorf("aggregated event = %#v", events[0])
	}
}

func TestListSandboxNodeLoads(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/namespaces/opensandbox/pods":
			_ = json.NewEncoder(w).Encode(podList{Items: []podResource{
				{
					Metadata: podMetadata{Labels: map[string]string{"opensandbox.io/id": "sandbox-1"}},
					Spec:     podSpec{NodeName: "node-a", Containers: []containerSpec{{Resources: containerResources{Requests: map[string]string{"cpu": "1", "memory": "2Gi"}}}}},
					Status:   podStatus{Phase: "Running"},
				},
				{
					Metadata: podMetadata{Labels: map[string]string{"app": "unrelated"}},
					Spec:     podSpec{NodeName: "node-a", Containers: []containerSpec{{Resources: containerResources{Requests: map[string]string{"cpu": "3", "memory": "4Gi"}}}}},
					Status:   podStatus{Phase: "Running"},
				},
			}})
		case "/api/v1/nodes":
			_ = json.NewEncoder(w).Encode(nodeList{Items: []nodeResource{{
				Metadata: nodeMetadata{Name: "node-a"},
				Status:   nodeStatus{Allocatable: map[string]string{"cpu": "4", "memory": "8Gi"}},
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := newClient(server.URL, server.Client(), Options{}, nil)
	if err != nil {
		t.Fatalf("newClient() error = %v", err)
	}
	loads, err := client.ListSandboxNodeLoads(context.Background())
	if err != nil {
		t.Fatalf("ListSandboxNodeLoads() error = %v", err)
	}
	if len(loads) != 1 {
		t.Fatalf("ListSandboxNodeLoads() = %#v", loads)
	}
	load := loads[0]
	if load.Name != "node-a" || load.SandboxCount != 1 || load.CPURequestedMilli != 1000 || load.CPUAllocatableMilli != 4000 || load.MemoryRequestedBytes != 2*1024*1024*1024 || load.MemoryAllocatableBytes != 8*1024*1024*1024 {
		t.Errorf("node load = %#v", load)
	}
}

func TestTerminalOperations(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/namespaces/control/secrets/api-key":
			_ = json.NewEncoder(w).Encode(kubeSecret{Data: map[string]string{
				"token": base64.StdEncoding.EncodeToString([]byte("test-key")),
			}})
		case "/api/v1/namespaces/control/services/http:lifecycle:http/proxy/sandboxes/sandbox-1/proxy/44772/pty":
			var request map[string]string
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode PTY request: %v", err)
			}
			if !strings.Contains(request["command"], "TERM=xterm-256color") {
				t.Errorf("PTY command = %q", request["command"])
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{"session_id": "pty-1"})
		case "/api/v1/namespaces/control/services/http:lifecycle:http/proxy/sandboxes/sandbox-1/proxy/44772/pty/pty-1/ws":
			if got := r.Header.Get(apiKeyHeader); got != "test-key" {
				t.Errorf("API key header = %q", got)
			}
			connection, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept WebSocket: %v", err)
				return
			}
			defer connection.Close(websocket.StatusNormalClosure, "")
			_ = connection.Write(r.Context(), websocket.MessageText, []byte(`{"type":"connected","mode":"pty"}`))
		case "/api/v1/namespaces/control/services/http:lifecycle:http/proxy/sandboxes/sandbox-1/proxy/44772/command":
			var request map[string]any
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode command request: %v", err)
			}
			if request["command"] != "echo stats" {
				t.Errorf("command = %q", request["command"])
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "event: stdout\ndata: cpu=12.5\\n\n\nevent: result\ndata: {\"exit_code\":0}\n\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := newClient(server.URL, server.Client(), Options{
		Namespace:        "control",
		ServiceName:      "lifecycle",
		ServicePort:      "http",
		APIKeySecretName: "api-key",
		APIKeySecretKey:  "token",
	}, nil)
	if err != nil {
		t.Fatalf("newClient() error = %v", err)
	}
	connection, err := client.OpenPTY(context.Background(), "sandbox-1")
	if err != nil {
		t.Fatalf("OpenPTY() error = %v", err)
	}
	defer connection.Close(websocket.StatusNormalClosure, "")

	messageType, message, err := connection.Read(context.Background())
	if err != nil {
		t.Fatalf("read PTY WebSocket: %v", err)
	}
	if messageType != websocket.MessageText || !strings.Contains(string(message), `"connected"`) {
		t.Errorf("PTY message = %q", message)
	}

	result, err := client.RunCommand(context.Background(), "sandbox-1", "echo stats")
	if err != nil {
		t.Fatalf("RunCommand() error = %v", err)
	}
	if result.Stdout != "cpu=12.5\\n\n" || result.ExitCode != 0 {
		t.Errorf("RunCommand() = %#v", result)
	}
}

func TestReadCommandStreamHandlesAdjacentNDJSONEvents(t *testing.T) {
	stream := strings.Join([]string{
		`{"type":"stdout","text":"one"}`,
		`{"type":"stdout","text":"two"}`,
		`{"type":"result","exit_code":0}`,
	}, "\n")
	result, err := readCommandStream(strings.NewReader(stream))
	if err != nil {
		t.Fatalf("readCommandStream() error = %v", err)
	}
	if result.Stdout != "one\ntwo\n" || result.ExitCode != 0 {
		t.Errorf("readCommandStream() = %#v", result)
	}
}

func TestCreateSandboxOmitsZeroTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/namespaces/control/secrets/api-key":
			_ = json.NewEncoder(w).Encode(kubeSecret{Data: map[string]string{
				"token": base64.StdEncoding.EncodeToString([]byte("test-key")),
			}})
		case "/api/v1/namespaces/control/services/http:lifecycle:http/proxy/sandboxes":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode request: %v", err)
			}
			if _, exists := payload["timeout"]; exists {
				t.Errorf("request unexpectedly contains timeout: %#v", payload)
			}
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(apiSandbox{
				ID:        "without-timeout",
				Status:    apiSandboxStatus{State: "Running"},
				CreatedAt: time.Now(),
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	combined, err := newClient(server.URL, server.Client(), Options{
		Namespace:        "control",
		ServiceName:      "lifecycle",
		ServicePort:      "http",
		APIKeySecretName: "api-key",
		APIKeySecretKey:  "token",
	}, nil)
	if err != nil {
		t.Fatalf("newClient() error = %v", err)
	}
	_, err = combined.CreateSandbox(context.Background(), CreateSandboxRequest{
		Image:          "python:3.12-slim",
		Entrypoint:     []string{"tail", "-f", "/dev/null"},
		ResourceLimits: map[string]string{"cpu": "1", "memory": "2Gi"},
	})
	if err != nil {
		t.Fatalf("CreateSandbox() error = %v", err)
	}
}

func TestDeleteSandboxFallsBackToCustomResource(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/apis/sandbox.opensandbox.io/v1alpha1/namespaces/workloads/batchsandboxes/direct" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "Success"})
	}))
	defer server.Close()

	combined, err := newClient(server.URL, server.Client(), Options{}, nil)
	if err != nil {
		t.Fatalf("newClient() error = %v", err)
	}
	err = combined.DeleteSandbox(context.Background(), Sandbox{
		ID:      "direct",
		Sources: []string{SourceBatchSandbox},
		Resources: []ResourceReference{{
			Source:    SourceBatchSandbox,
			Group:     "sandbox.opensandbox.io",
			Version:   "v1alpha1",
			Plural:    "batchsandboxes",
			Namespace: "workloads",
			Name:      "direct",
		}},
	})
	if err != nil {
		t.Fatalf("DeleteSandbox() error = %v", err)
	}
}

func TestSnapshotOperations(t *testing.T) {
	createdAt := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/namespaces/control/secrets/api-key":
			_ = json.NewEncoder(w).Encode(kubeSecret{Data: map[string]string{
				"token": base64.StdEncoding.EncodeToString([]byte("test-key")),
			}})
		case r.URL.Path == "/api/v1/namespaces/control/services/http:lifecycle:http/proxy/snapshots" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(snapshotListResponse{
				Items: []apiSnapshot{{
					ID:        "snapshot-1",
					SandboxID: "sandbox-1",
					Name:      "checkpoint",
					Status:    apiSnapshotStatus{State: "Ready"},
					CreatedAt: createdAt,
				}},
			})
		case r.URL.Path == "/api/v1/namespaces/control/services/http:lifecycle:http/proxy/sandboxes/sandbox-1/snapshots" && r.Method == http.MethodPost:
			var request apiCreateSnapshotRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode snapshot request: %v", err)
			}
			if request.Name != "checkpoint" {
				t.Errorf("snapshot name = %q", request.Name)
			}
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(apiSnapshot{
				ID:        "snapshot-2",
				SandboxID: "sandbox-1",
				Name:      request.Name,
				Status:    apiSnapshotStatus{State: "Creating"},
				CreatedAt: createdAt,
			})
		case r.URL.Path == "/api/v1/namespaces/control/services/http:lifecycle:http/proxy/snapshots/snapshot-1" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(apiSnapshot{
				ID:        "snapshot-1",
				SandboxID: "sandbox-1",
				Name:      "checkpoint",
				Status:    apiSnapshotStatus{State: "Ready"},
				CreatedAt: createdAt,
			})
		case r.URL.Path == "/api/v1/namespaces/control/services/http:lifecycle:http/proxy/snapshots/snapshot-1" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	combined, err := newClient(server.URL, server.Client(), Options{
		Namespace:        "control",
		ServiceName:      "lifecycle",
		ServicePort:      "http",
		APIKeySecretName: "api-key",
		APIKeySecretKey:  "token",
	}, nil)
	if err != nil {
		t.Fatalf("newClient() error = %v", err)
	}
	snapshots, err := combined.ListSnapshots(context.Background())
	if err != nil || len(snapshots) != 1 || snapshots[0].State != "Ready" {
		t.Fatalf("ListSnapshots() = %#v, %v", snapshots, err)
	}
	got, err := combined.GetSnapshot(context.Background(), "snapshot-1")
	if err != nil || got.ID != "snapshot-1" || got.Name != "checkpoint" {
		t.Fatalf("GetSnapshot() = %#v, %v", got, err)
	}
	created, err := combined.CreateSnapshot(context.Background(), "sandbox-1", "checkpoint")
	if err != nil || created.ID != "snapshot-2" || created.State != "Creating" {
		t.Fatalf("CreateSnapshot() = %#v, %v", created, err)
	}
	if err := combined.DeleteSnapshot(context.Background(), "snapshot-1"); err != nil {
		t.Fatalf("DeleteSnapshot() error = %v", err)
	}
}

func TestCreateSandboxValidation(t *testing.T) {
	client := &client{}
	_, err := client.CreateSandbox(context.Background(), CreateSandboxRequest{})
	if err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("CreateSandbox() error = %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func assertAPIKey(t *testing.T, request *http.Request) {
	t.Helper()
	if got := request.Header.Get(apiKeyHeader); got != "test-key" {
		t.Errorf("API key header = %q", got)
	}
}
