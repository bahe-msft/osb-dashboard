package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestNewOverviewDataGroupsSandboxesByState(t *testing.T) {
	data := newOverviewData([]sandboxView{
		{Name: "sandbox-paused", State: "Paused"},
		{Name: "sandbox-failed", State: "failed"},
		{Name: "sandbox-running", State: " running "},
	})

	if data.Total != 3 {
		t.Fatalf("Total = %d, want 3", data.Total)
	}

	wantStates := []string{"running", "paused", "failed"}
	if len(data.Groups) != len(wantStates) {
		t.Fatalf("len(Groups) = %d, want %d", len(data.Groups), len(wantStates))
	}
	for index, wantState := range wantStates {
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
}

func TestRoutes(t *testing.T) {
	app, err := newApplication("/tmp/test-kubeconfig")
	if err != nil {
		t.Fatalf("newApplication() error = %v", err)
	}

	tests := []struct {
		name           string
		path           string
		contentType    string
		contains       string
		doesNotContain string
	}{
		{
			name:        "dashboard page",
			path:        "/",
			contentType: "text/html; charset=utf-8",
			contains:    "hx-get=\"/dashboard/overview\"",
		},
		{
			name:           "overview fragment",
			path:           "/dashboard/overview",
			contentType:    "text/html; charset=utf-8",
			contains:       "Deploy a new sandbox",
			doesNotContain: "OpenSandbox API not configured",
		},
		{
			name:        "favicon asset",
			path:        "/assets/favicon.svg",
			contentType: "image/svg+xml",
			contains:    "<svg",
		},
		{
			name:        "health check",
			path:        "/healthz",
			contentType: "text/plain; charset=utf-8",
			contains:    "ok\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			response := httptest.NewRecorder()

			app.routes().ServeHTTP(response, request)

			result := response.Result()
			defer result.Body.Close()

			if result.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want %d", result.StatusCode, http.StatusOK)
			}
			if got := result.Header.Get("Content-Type"); got != test.contentType {
				t.Errorf("Content-Type = %q, want %q", got, test.contentType)
			}

			body, err := io.ReadAll(result.Body)
			if err != nil {
				t.Fatalf("read response body: %v", err)
			}
			bodyText := string(body)
			if !strings.Contains(bodyText, test.contains) {
				t.Errorf("response body does not contain %q", test.contains)
			}
			if test.doesNotContain != "" && strings.Contains(bodyText, test.doesNotContain) {
				t.Errorf("response body unexpectedly contains %q", test.doesNotContain)
			}
		})
	}
}
