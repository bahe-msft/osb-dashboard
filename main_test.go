package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRoutes(t *testing.T) {
	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication() error = %v", err)
	}

	tests := []struct {
		name        string
		path        string
		contentType string
		contains    string
	}{
		{
			name:        "dashboard page",
			path:        "/",
			contentType: "text/html; charset=utf-8",
			contains:    "hx-get=\"/dashboard/overview\"",
		},
		{
			name:        "overview fragment",
			path:        "/dashboard/overview",
			contentType: "text/html; charset=utf-8",
			contains:    "OpenSandbox API not configured",
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
			if !strings.Contains(string(body), test.contains) {
				t.Errorf("response body does not contain %q", test.contains)
			}
		})
	}
}
