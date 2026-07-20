package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bahe-msft/osb-dashboard/internal/opensandbox"
)

type fakeSandboxService struct {
	sandboxes []opensandbox.Sandbox
}

func (service *fakeSandboxService) ListSandboxes(context.Context) ([]opensandbox.Sandbox, error) {
	return append([]opensandbox.Sandbox(nil), service.sandboxes...), nil
}

func (service *fakeSandboxService) CreateSandbox(_ context.Context, request opensandbox.CreateSandboxRequest) (opensandbox.Sandbox, error) {
	sandbox := opensandbox.Sandbox{
		ID:        "sandbox-created",
		State:     "Pending",
		CreatedAt: time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC),
		Image:     request.Image,
		Metadata:  request.Metadata,
	}
	service.sandboxes = append(service.sandboxes, sandbox)
	return sandbox, nil
}

func (service *fakeSandboxService) DeleteSandbox(_ context.Context, sandbox opensandbox.Sandbox) error {
	for index := range service.sandboxes {
		if service.sandboxes[index].ID != sandbox.ID {
			continue
		}
		service.sandboxes = append(service.sandboxes[:index], service.sandboxes[index+1:]...)
		break
	}
	return nil
}

func TestParseCommandConfig(t *testing.T) {
	kubeconfigPath := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(kubeconfigPath, []byte("apiVersion: v1\n"), 0o600); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}

	config, err := parseCommandConfig([]string{"--kubeconfig", kubeconfigPath})
	if err != nil {
		t.Fatalf("parseCommandConfig() error = %v", err)
	}
	if config.kubeconfigPath != kubeconfigPath {
		t.Errorf("kubeconfigPath = %q, want %q", config.kubeconfigPath, kubeconfigPath)
	}
}

func TestParseCommandConfigRejectsInvalidArguments(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "missing kubeconfig argument",
			want: "--kubeconfig is required",
		},
		{
			name: "missing kubeconfig file",
			args: []string{"--kubeconfig", filepath.Join(t.TempDir(), "missing")},
			want: "open kubeconfig",
		},
		{
			name: "unexpected positional argument",
			args: []string{"--kubeconfig", "config", "extra"},
			want: "unexpected positional arguments",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseCommandConfig(test.args)
			if err == nil {
				t.Fatal("parseCommandConfig() error = nil, want error")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Errorf("error = %q, want it to contain %q", err, test.want)
			}
		})
	}
}

func TestCreateSandboxRequest(t *testing.T) {
	app := &application{sandboxImage: "default:image"}
	form := url.Values{
		"image":          {"custom:image"},
		"resourcePreset": {"2core-4gib"},
	}
	request := httptest.NewRequest(http.MethodPost, "/dashboard/sandboxes", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	result, err := app.createSandboxRequest(request)
	if err != nil {
		t.Fatalf("createSandboxRequest() error = %v", err)
	}
	if result.Image != "custom:image" || result.Timeout != 0 {
		t.Errorf("createSandboxRequest() = %#v", result)
	}
	if result.ResourceLimits["cpu"] != "2" || result.ResourceLimits["memory"] != "4Gi" {
		t.Errorf("ResourceLimits = %#v", result.ResourceLimits)
	}
	if result.Metadata["createdBy"] != "osb-dashboard" {
		t.Errorf("Metadata = %#v", result.Metadata)
	}
}

func TestCreateSandboxRequestRejectsInvalidResourcePreset(t *testing.T) {
	app := &application{sandboxImage: "default:image"}
	request := httptest.NewRequest(http.MethodPost, "/dashboard/sandboxes", strings.NewReader("resourcePreset=invalid"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	_, err := app.createSandboxRequest(request)
	if err == nil || !strings.Contains(err.Error(), "valid resource preset") {
		t.Fatalf("createSandboxRequest() error = %v", err)
	}
}

func TestFormatSandboxResources(t *testing.T) {
	tests := []struct {
		cpu    string
		memory string
		want   string
	}{
		{cpu: "1", memory: "2Gi", want: "1 core / 2 GiB"},
		{cpu: "4", memory: "8Gi", want: "4 cores / 8 GiB"},
		{cpu: "250m", memory: "512Mi", want: "250m CPU / 512 MiB"},
		{want: "—"},
	}
	for _, test := range tests {
		if got := formatSandboxResources(test.cpu, test.memory); got != test.want {
			t.Errorf("formatSandboxResources(%q, %q) = %q, want %q", test.cpu, test.memory, got, test.want)
		}
	}
}

func TestNewOverviewDataGroupsSandboxesByState(t *testing.T) {
	data := newOverviewData([]sandboxView{
		{Name: "sandbox-paused", State: "Paused"},
		{Name: "sandbox-failed", State: "failed"},
		{Name: "sandbox-running", State: " running "},
		{Name: "sandbox-pending", State: "pending"},
	})

	if data.Total != 4 {
		t.Fatalf("Total = %d, want 4", data.Total)
	}

	wantStates := []string{"running", "pending", "paused", "failed"}
	if len(data.StateCounts) != len(wantStates) {
		t.Fatalf("len(StateCounts) = %d, want %d", len(data.StateCounts), len(wantStates))
	}
	if len(data.Groups) != len(wantStates) {
		t.Fatalf("len(Groups) = %d, want %d", len(data.Groups), len(wantStates))
	}
	for index, wantState := range wantStates {
		if data.StateCounts[index].State != wantState || data.StateCounts[index].Count != 1 {
			t.Errorf("StateCounts[%d] = %#v, want state %q with count 1", index, data.StateCounts[index], wantState)
		}
		group := data.Groups[index]
		if group.State != wantState {
			t.Errorf("Groups[%d].State = %q, want %q", index, group.State, wantState)
		}
		if len(group.Sandboxes) != 1 {
			t.Errorf("len(Groups[%d].Sandboxes) = %d, want 1", index, len(group.Sandboxes))
		}
		if group.Sandboxes[0].State != wantState {
			t.Errorf("Groups[%d].Sandboxes[0].State = %q, want %q", index, group.Sandboxes[0].State, wantState)
		}
	}
}

func TestNewOverviewDataUsesEmptyStateWhenThereAreNoSandboxes(t *testing.T) {
	data := newOverviewData(nil)

	if data.Total != 0 {
		t.Errorf("Total = %d, want 0", data.Total)
	}
	if len(data.Groups) != 0 {
		t.Errorf("len(Groups) = %d, want 0", len(data.Groups))
	}
	if len(data.StateCounts) != 0 {
		t.Errorf("len(StateCounts) = %d, want 0", len(data.StateCounts))
	}
}

func TestRoutes(t *testing.T) {
	service := &fakeSandboxService{}
	app, err := newApplication(
		"/tmp/test-kubeconfig",
		service,
		service,
		"python:3.12-slim",
		context.Background(),
	)
	if err != nil {
		t.Fatalf("newApplication() error = %v", err)
	}

	tests := []struct {
		name           string
		method         string
		path           string
		contentType    string
		contains       string
		doesNotContain string
	}{
		{
			name:        "dashboard page",
			method:      http.MethodGet,
			path:        "/",
			contentType: "text/html; charset=utf-8",
			contains:    "hx-get=\"/dashboard/overview\"",
		},
		{
			name:           "overview fragment",
			method:         http.MethodGet,
			path:           "/dashboard/overview",
			contentType:    "text/html; charset=utf-8",
			contains:       "Deploy a new sandbox",
			doesNotContain: "OpenSandbox API not configured",
		},
		{
			name:        "create sandbox",
			method:      http.MethodPost,
			path:        "/dashboard/sandboxes",
			contentType: "text/html; charset=utf-8",
			contains:    "sandbox-created",
		},
		{
			name:        "delete sandbox",
			method:      http.MethodDelete,
			path:        "/dashboard/sandboxes/sandbox-created",
			contentType: "text/html; charset=utf-8",
			contains:    "Deploy a new sandbox",
		},
		{
			name:        "favicon asset",
			method:      http.MethodGet,
			path:        "/assets/favicon.svg",
			contentType: "image/svg+xml",
			contains:    "<svg",
		},
		{
			name:        "health check",
			method:      http.MethodGet,
			path:        "/healthz",
			contentType: "text/plain; charset=utf-8",
			contains:    "ok\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, nil)
			response := httptest.NewRecorder()

			app.routes().ServeHTTP(response, request)
			if test.method == http.MethodPost {
				app.background.Wait()
			}

			result := response.Result()
			defer result.Body.Close()

			if result.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want %d", result.StatusCode, http.StatusOK)
			}
			if test.method == http.MethodPost {
				if got := result.Header.Get("HX-Trigger"); got != "sandboxCreateAccepted" {
					t.Errorf("HX-Trigger = %q, want %q", got, "sandboxCreateAccepted")
				}
			}
			if test.contentType != "" {
				if got := result.Header.Get("Content-Type"); got != test.contentType {
					t.Errorf("Content-Type = %q, want %q", got, test.contentType)
				}
			}

			body, err := io.ReadAll(result.Body)
			if err != nil {
				t.Fatalf("read response body: %v", err)
			}
			bodyText := string(body)
			if test.contains != "" && !strings.Contains(bodyText, test.contains) {
				t.Errorf("response body does not contain %q", test.contains)
			}
			if test.doesNotContain != "" && strings.Contains(bodyText, test.doesNotContain) {
				t.Errorf("response body unexpectedly contains %q", test.doesNotContain)
			}
		})
	}
}
