package dashboard_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dashboard "github.com/bahe-msft/osb-dashboard"
	"github.com/bahe-msft/osb-dashboard/opensandbox"
	"github.com/coder/websocket"
)

type libraryClient struct{}

func (libraryClient) ListSandboxes(context.Context) ([]opensandbox.Sandbox, error) {
	return nil, nil
}

func (libraryClient) ListSnapshots(context.Context) ([]opensandbox.Snapshot, error) {
	return nil, nil
}

func (libraryClient) GetSnapshot(context.Context, string) (opensandbox.Snapshot, error) {
	return opensandbox.Snapshot{}, nil
}

func (libraryClient) ListSandboxNodeLoads(context.Context) ([]opensandbox.SandboxNodeLoad, error) {
	return nil, nil
}

func (libraryClient) ListPodEvents(context.Context, string) ([]opensandbox.SandboxEvent, error) {
	return nil, nil
}

func (libraryClient) ListRecentSandboxEvents(context.Context, []opensandbox.Sandbox) ([]opensandbox.SandboxEvent, error) {
	return nil, nil
}

func (libraryClient) CreateSandbox(context.Context, opensandbox.CreateSandboxRequest) (opensandbox.Sandbox, error) {
	return opensandbox.Sandbox{}, nil
}

func (libraryClient) DeleteSandbox(context.Context, opensandbox.Sandbox) error { return nil }
func (libraryClient) PauseSandbox(context.Context, string) error               { return nil }
func (libraryClient) ResumeSandbox(context.Context, string) error              { return nil }

func (libraryClient) CreateSnapshot(context.Context, string, string) (opensandbox.Snapshot, error) {
	return opensandbox.Snapshot{}, nil
}

func (libraryClient) DeleteSnapshot(context.Context, string) error { return nil }

func (libraryClient) OpenPTY(context.Context, string) (*websocket.Conn, error) {
	return nil, nil
}

func (libraryClient) RunCommand(context.Context, string, string) (opensandbox.CommandResult, error) {
	return opensandbox.CommandResult{}, nil
}

func TestLibraryHandlerIncludesAssetsAndAllowsAdditionalRoutes(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app, err := dashboard.New(libraryClient{}, dashboard.Options{
		BasePath: "/dashboard",
		Logger:   logger,
		RegisterRoutes: func(mux *http.ServeMux) {
			mux.HandleFunc("GET /custom", func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, "custom route")
			})
		},
	})
	if err != nil {
		t.Fatalf("dashboard.New() error = %v", err)
	}
	defer app.Close()

	for _, test := range []struct {
		path            string
		wantStatus      int
		wantContent     string
		wantContentType string
	}{
		{path: "/dashboard/custom", wantStatus: http.StatusOK, wantContent: "custom route"},
		{path: "/dashboard/assets/favicon.svg", wantStatus: http.StatusOK, wantContent: "<svg"},
		{path: "/dashboard/assets/third-party/ui/htmx.min.js", wantStatus: http.StatusOK, wantContent: "var htmx=", wantContentType: "text/javascript"},
		{path: "/dashboard/assets/third-party/ghostty-web/ghostty-web.js", wantStatus: http.StatusOK, wantContent: "ghostty_terminal", wantContentType: "text/javascript"},
		{path: "/dashboard/assets/third-party/ghostty-web/ghostty-vt.wasm", wantStatus: http.StatusOK, wantContent: "\x00asm", wantContentType: "application/wasm"},
		{path: "/dashboard/", wantStatus: http.StatusOK, wantContent: "OpenSandbox"},
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, test.path, nil)
		app.Handler().ServeHTTP(response, request)
		if response.Code != test.wantStatus {
			t.Errorf("GET %s status = %d, want %d", test.path, response.Code, test.wantStatus)
		}
		if !strings.Contains(response.Body.String(), test.wantContent) {
			t.Errorf("GET %s body does not contain %q", test.path, test.wantContent)
		}
		if test.wantContentType != "" && !strings.HasPrefix(response.Header().Get("Content-Type"), test.wantContentType) {
			t.Errorf("GET %s Content-Type = %q, want prefix %q", test.path, response.Header().Get("Content-Type"), test.wantContentType)
		}
	}
}
