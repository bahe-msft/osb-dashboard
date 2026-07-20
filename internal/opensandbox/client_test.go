package opensandbox

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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
			if request.Image.URI != "python:3.12-slim" {
				t.Errorf("image = %q", request.Image.URI)
			}
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(apiSandbox{
				ID:        "sandbox-2",
				Status:    apiSandboxStatus{State: "Pending"},
				CreatedAt: time.Date(2026, time.July, 19, 12, 1, 0, 0, time.UTC),
			})
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

func TestCreateSandboxValidation(t *testing.T) {
	client := &client{}
	_, err := client.CreateSandbox(context.Background(), CreateSandboxRequest{})
	if err == nil || !strings.Contains(err.Error(), "image is required") {
		t.Fatalf("CreateSandbox() error = %v", err)
	}
}

func assertAPIKey(t *testing.T, request *http.Request) {
	t.Helper()
	if got := request.Header.Get(apiKeyHeader); got != "test-key" {
		t.Errorf("API key header = %q", got)
	}
}
