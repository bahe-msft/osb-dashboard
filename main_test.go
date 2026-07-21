package main

import (
	"context"
	"errors"
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
	"github.com/coder/websocket"
)

type fakeSandboxService struct {
	sandboxes []opensandbox.Sandbox
	snapshots []opensandbox.Snapshot
}

type noBashSandboxService struct {
	*fakeSandboxService
}

func (service *noBashSandboxService) RunCommand(ctx context.Context, sandboxID, command string) (opensandbox.CommandResult, error) {
	if command == terminalShellProbeCommand {
		return opensandbox.CommandResult{ExitCode: 1}, nil
	}
	return service.fakeSandboxService.RunCommand(ctx, sandboxID, command)
}

func (service *fakeSandboxService) ListSandboxes(context.Context) ([]opensandbox.Sandbox, error) {
	return append([]opensandbox.Sandbox(nil), service.sandboxes...), nil
}

func (service *fakeSandboxService) ListSnapshots(context.Context) ([]opensandbox.Snapshot, error) {
	return append([]opensandbox.Snapshot(nil), service.snapshots...), nil
}

func (service *fakeSandboxService) GetSnapshot(_ context.Context, snapshotID string) (opensandbox.Snapshot, error) {
	for _, snapshot := range service.snapshots {
		if snapshot.ID == snapshotID {
			return snapshot, nil
		}
	}
	return opensandbox.Snapshot{}, errors.New("snapshot not found")
}

func (service *fakeSandboxService) CreateSandbox(_ context.Context, request opensandbox.CreateSandboxRequest) (opensandbox.Sandbox, error) {
	image := request.Image
	state := "Running"
	if request.SnapshotID != "" {
		image = "restored:" + request.SnapshotID
		state = "Pending"
	}
	sandbox := opensandbox.Sandbox{
		ID:        "sandbox-created",
		State:     state,
		CreatedAt: time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC),
		Image:     image,
		Metadata:  request.Metadata,
		Sources:   []string{opensandbox.SourceLifecycle},
	}
	service.sandboxes = append(service.sandboxes, sandbox)
	return sandbox, nil
}

func (service *fakeSandboxService) OpenPTY(context.Context, string) (*websocket.Conn, error) {
	return nil, nil
}

func (service *fakeSandboxService) PauseSandbox(_ context.Context, sandboxID string) error {
	for index := range service.sandboxes {
		if service.sandboxes[index].ID == sandboxID {
			service.sandboxes[index].State = "Paused"
		}
	}
	return nil
}

func (service *fakeSandboxService) ResumeSandbox(_ context.Context, sandboxID string) error {
	for index := range service.sandboxes {
		if service.sandboxes[index].ID == sandboxID {
			service.sandboxes[index].State = "Running"
		}
	}
	return nil
}

func (service *fakeSandboxService) CreateSnapshot(_ context.Context, sandboxID, name string) (opensandbox.Snapshot, error) {
	snapshot := opensandbox.Snapshot{
		ID:        "snapshot-created",
		SandboxID: sandboxID,
		Name:      name,
		State:     "Creating",
		CreatedAt: time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC),
	}
	service.snapshots = append(service.snapshots, snapshot)
	return snapshot, nil
}

func (service *fakeSandboxService) DeleteSnapshot(_ context.Context, snapshotID string) error {
	for index := range service.snapshots {
		if service.snapshots[index].ID == snapshotID {
			service.snapshots = append(service.snapshots[:index], service.snapshots[index+1:]...)
			break
		}
	}
	return nil
}

func (service *fakeSandboxService) RunCommand(context.Context, string, string) (opensandbox.CommandResult, error) {
	return opensandbox.CommandResult{Stdout: strings.Join([]string{
		"cpu_unit=us",
		"cpu_start=100000",
		"cpu_end=162500",
		"cpu_quota=200000",
		"cpu_period=100000",
		"cpu_count=2",
		"load_1=0.50",
		"memory_current=536870912",
		"memory_max=4294967296",
	}, "\n")}, nil
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

	config, err := parseCommandConfig([]string{"--kubeconfig", kubeconfigPath, "--base-path", "dashboard/"})
	if err != nil {
		t.Fatalf("parseCommandConfig() error = %v", err)
	}
	if config.kubeconfigPath != kubeconfigPath {
		t.Errorf("kubeconfigPath = %q, want %q", config.kubeconfigPath, kubeconfigPath)
	}
	if config.basePath != "/dashboard" {
		t.Errorf("basePath = %q, want %q", config.basePath, "/dashboard")
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

func TestNormalizeBasePath(t *testing.T) {
	tests := []struct {
		value string
		want  string
		err   bool
	}{
		{value: "", want: ""},
		{value: "/", want: ""},
		{value: "dashboard", want: "/dashboard"},
		{value: "/dashboard/", want: "/dashboard"},
		{value: "/nested//dashboard", want: "/nested/dashboard"},
		{value: "/../dashboard", err: true},
		{value: "/dashboard?mode=test", err: true},
	}
	for _, test := range tests {
		got, err := normalizeBasePath(test.value)
		if (err != nil) != test.err {
			t.Errorf("normalizeBasePath(%q) error = %v, want error %t", test.value, err, test.err)
		}
		if got != test.want {
			t.Errorf("normalizeBasePath(%q) = %q, want %q", test.value, got, test.want)
		}
	}
}

func TestLoopbackAddressValidation(t *testing.T) {
	for _, address := range []string{"127.0.0.1:8080", "[::1]:8080", "localhost:8080"} {
		if !isLoopbackAddress(address) {
			t.Errorf("isLoopbackAddress(%q) = false", address)
		}
	}
	for _, address := range []string{":8080", "0.0.0.0:8080", "example.com:8080", "invalid"} {
		if isLoopbackAddress(address) {
			t.Errorf("isLoopbackAddress(%q) = true", address)
		}
	}
}

func TestTokenAuthenticationAndCSRFProtection(t *testing.T) {
	handler := securityHeaders(tokenAuthentication("secret", csrfProtection(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))))

	tests := []struct {
		name       string
		method     string
		auth       string
		origin     string
		csrfHeader string
		want       int
	}{
		{name: "missing authentication", method: http.MethodGet, want: http.StatusUnauthorized},
		{name: "authenticated read", method: http.MethodGet, auth: "Bearer secret", want: http.StatusNoContent},
		{name: "mutation without origin", method: http.MethodPost, auth: "Bearer secret", want: http.StatusForbidden},
		{name: "same-origin browser mutation", method: http.MethodDelete, auth: "Bearer secret", origin: "http://dashboard.test", want: http.StatusNoContent},
		{name: "cross-origin browser mutation", method: http.MethodPost, auth: "Bearer secret", origin: "https://attacker.test", want: http.StatusForbidden},
		{name: "explicit API CSRF header", method: http.MethodPost, auth: "Bearer secret", csrfHeader: "1", want: http.StatusNoContent},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, "http://dashboard.test/dashboard/sandboxes", nil)
			request.Header.Set("Authorization", test.auth)
			request.Header.Set("Origin", test.origin)
			request.Header.Set("X-OSB-CSRF", test.csrfHeader)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Errorf("status = %d, want %d", response.Code, test.want)
			}
			if got := response.Header().Get("Content-Security-Policy"); !strings.Contains(got, "default-src 'self'") {
				t.Errorf("Content-Security-Policy = %q", got)
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

func TestCreateSandboxRequestRestoresSnapshot(t *testing.T) {
	app := &application{sandboxImage: "default:image"}
	form := url.Values{
		"snapshotId":     {"snapshot-ready"},
		"resourcePreset": {"2core-4gib"},
	}
	request := httptest.NewRequest(http.MethodPost, "/dashboard/sandboxes", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	result, err := app.createSandboxRequest(request)
	if err != nil {
		t.Fatalf("createSandboxRequest() error = %v", err)
	}
	if result.SnapshotID != "snapshot-ready" || result.Image != "" || len(result.Entrypoint) != 0 {
		t.Errorf("createSandboxRequest() = %#v", result)
	}
	if result.ResourceLimits["cpu"] != "2" || result.ResourceLimits["memory"] != "4Gi" {
		t.Errorf("ResourceLimits = %#v", result.ResourceLimits)
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

func TestSandboxDetailFromSandboxHidesInternalMetadata(t *testing.T) {
	data := sandboxDetailFromSandbox(opensandbox.Sandbox{
		ID:      "sandbox-1",
		State:   "Running",
		Sources: []string{opensandbox.SourceLifecycle, opensandbox.SourceBatchSandbox},
		Metadata: map[string]string{
			"createdBy":                "osb-dashboard",
			"osb-dashboard/request-id": "request-id",
			"team":                     "platform",
		},
	})

	if data.Sources != "Lifecycle API + BatchSandbox" {
		t.Errorf("Sources = %q", data.Sources)
	}
	if len(data.Metadata) != 1 || data.Metadata[0].Label != "team" || data.Metadata[0].Value != "platform" {
		t.Errorf("Metadata = %#v", data.Metadata)
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

func TestTerminalPropagatesMissingBashError(t *testing.T) {
	service := &noBashSandboxService{fakeSandboxService: &fakeSandboxService{}}
	app, err := newApplication(
		"/tmp/test-kubeconfig",
		service,
		service,
		service,
		service,
		"python:3.12-slim",
		context.Background(),
	)
	if err != nil {
		t.Fatalf("newApplication() error = %v", err)
	}
	server := httptest.NewServer(app.routes())
	defer server.Close()

	websocketURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/dashboard/sandboxes/no-bash/terminal/pty"
	connection, _, err := websocket.Dial(context.Background(), websocketURL, nil)
	if err != nil {
		t.Fatalf("dial terminal WebSocket: %v", err)
	}
	defer connection.Close(websocket.StatusNormalClosure, "")

	messageType, message, err := connection.Read(context.Background())
	if err != nil {
		t.Fatalf("read terminal error: %v", err)
	}
	if messageType != websocket.MessageText || !strings.Contains(string(message), "Bash is not installed") {
		t.Errorf("terminal message = %q", message)
	}
}

func TestSnapshotRoutes(t *testing.T) {
	service := &fakeSandboxService{sandboxes: []opensandbox.Sandbox{{
		ID:        "sandbox-running",
		State:     "Running",
		CreatedAt: time.Date(2026, time.July, 20, 11, 0, 0, 0, time.UTC),
		Sources:   []string{opensandbox.SourceLifecycle},
	}}}
	app, err := newApplication(
		"/tmp/test-kubeconfig",
		service,
		service,
		service,
		service,
		"python:3.12-slim",
		context.Background(),
	)
	if err != nil {
		t.Fatalf("newApplication() error = %v", err)
	}

	form := url.Values{"sandboxID": {"sandbox-running"}, "name": {"before-upgrade"}}
	request := httptest.NewRequest(http.MethodPost, "/dashboard/snapshots", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	app.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("create snapshot status = %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Header().Get("HX-Trigger"); got != "snapshotCreated" {
		t.Errorf("HX-Trigger = %q", got)
	}
	if got := response.Header().Get("HX-Push-Url"); got != "" {
		t.Errorf("HX-Push-Url = %q, want empty", got)
	}
	if !strings.Contains(response.Body.String(), "Creating snapshot") || !strings.Contains(response.Body.String(), "before-upgrade") || !strings.Contains(response.Body.String(), "Run in background") || !strings.Contains(response.Body.String(), "hx-trigger=\"load delay:2s\"") {
		t.Errorf("create snapshot response = %s", response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/dashboard/snapshots/snapshot-created/status", nil)
	response = httptest.NewRecorder()
	app.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Creating snapshot") || !strings.Contains(response.Body.String(), "hx-trigger=\"load delay:2s\"") {
		t.Errorf("creating snapshot status = %d, response = %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/snapshots/snapshot-created", nil)
	response = httptest.NewRecorder()
	app.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "hx-get=\"/dashboard/snapshots/snapshot-created/fragment\"") {
		t.Errorf("snapshot detail page status = %d, response = %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/dashboard/snapshots/snapshot-created/fragment", nil)
	response = httptest.NewRecorder()
	app.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "before-upgrade") || !strings.Contains(response.Body.String(), "Snapshot details") {
		t.Errorf("snapshot detail fragment status = %d, response = %s", response.Code, response.Body.String())
	}

	service.snapshots[0].State = "Ready"
	service.snapshots[0].Message = "Snapshot is ready."
	request = httptest.NewRequest(http.MethodGet, "/dashboard/snapshots/snapshot-created/status", nil)
	response = httptest.NewRecorder()
	app.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Snapshot ready") || strings.Contains(response.Body.String(), "hx-trigger=\"load delay:2s\"") {
		t.Errorf("ready snapshot status = %d, response = %s", response.Code, response.Body.String())
	}

	deployForm := url.Values{
		"snapshotId":     {"snapshot-created"},
		"snapshotName":   {"before-upgrade"},
		"resourcePreset": {"1core-2gib"},
	}
	request = httptest.NewRequest(http.MethodPost, "/dashboard/sandboxes", strings.NewReader(deployForm.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response = httptest.NewRecorder()
	app.routes().ServeHTTP(response, request)
	app.background.Wait()
	if response.Code != http.StatusOK || response.Header().Get("HX-Trigger") != "sandboxDeploymentStarted" || !strings.Contains(response.Body.String(), "Deploying sandbox from snapshot") || !strings.Contains(response.Body.String(), "Run in background") || !strings.Contains(response.Body.String(), "load delay:2s") {
		t.Errorf("deploy snapshot status = %d, headers = %#v, response = %s", response.Code, response.Header(), response.Body.String())
	}
	if got := response.Header().Get("HX-Push-Url"); got != "" {
		t.Errorf("deploy snapshot HX-Push-Url = %q, want empty", got)
	}

	request = httptest.NewRequest(http.MethodGet, "/dashboard/sandboxes/sandbox-created/deployment-status?snapshotId=snapshot-created&snapshotName=before-upgrade", nil)
	response = httptest.NewRecorder()
	app.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Deploying sandbox from snapshot") || !strings.Contains(response.Body.String(), "Run in background") {
		t.Errorf("pending deployment status = %d, response = %s", response.Code, response.Body.String())
	}
	for index := range service.sandboxes {
		if service.sandboxes[index].ID == "sandbox-created" {
			service.sandboxes[index].State = "Running"
		}
	}
	request = httptest.NewRequest(http.MethodGet, "/dashboard/sandboxes/sandbox-created/deployment-status?snapshotId=snapshot-created&snapshotName=before-upgrade", nil)
	response = httptest.NewRecorder()
	app.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Sandbox deployed") || !strings.Contains(response.Body.String(), "View sandbox") || strings.Contains(response.Body.String(), "load delay:2s") {
		t.Errorf("ready deployment status = %d, response = %s", response.Code, response.Body.String())
	}

	app.invalidateSnapshotCache()
	request = httptest.NewRequest(http.MethodDelete, "/dashboard/snapshots/snapshot-created", nil)
	response = httptest.NewRecorder()
	app.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("delete snapshot status = %d, want %d", response.Code, http.StatusOK)
	}
	if len(service.snapshots) != 0 || !strings.Contains(response.Body.String(), "No snapshots yet") {
		t.Errorf("delete snapshot response = %s; snapshots = %#v", response.Body.String(), service.snapshots)
	}
}

func TestBasePathRoutes(t *testing.T) {
	service := &fakeSandboxService{sandboxes: []opensandbox.Sandbox{{
		ID:        "sandbox-1",
		State:     "Running",
		CreatedAt: time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC),
		Image:     "python:3.12-slim",
		Sources:   []string{opensandbox.SourceLifecycle},
	}}}
	app, err := newApplication(
		"/tmp/test-kubeconfig",
		service,
		service,
		service,
		service,
		"python:3.12-slim",
		context.Background(),
		"/dashboard",
	)
	if err != nil {
		t.Fatalf("newApplication() error = %v", err)
	}

	tests := []struct {
		path     string
		status   int
		contains []string
		location string
	}{
		{path: "/", status: http.StatusNotFound},
		{path: "/dashboard", status: http.StatusPermanentRedirect, location: "/dashboard/"},
		{path: "/dashboard/", status: http.StatusOK, contains: []string{
			`data-base-path="/dashboard"`,
			`src="/dashboard/assets/boot.js"`,
			`hx-get="/dashboard/dashboard/overview"`,
		}},
		{path: "/dashboard/assets/favicon.svg", status: http.StatusOK, contains: []string{"<svg"}},
		{path: "/dashboard/dashboard/overview", status: http.StatusOK, contains: []string{
			`hx-get="/dashboard/dashboard/sandboxes/sandbox-1/fragment"`,
			`hx-push-url="/dashboard/sandboxes/sandbox-1"`,
		}},
		{path: "/dashboard/snapshots", status: http.StatusOK, contains: []string{
			`data-page="snapshots"`,
			`hx-get="/dashboard/dashboard/snapshots"`,
		}},
		{path: "/dashboard/sandboxes/sandbox-1", status: http.StatusOK, contains: []string{
			`data-page="detail"`,
			`hx-get="/dashboard/dashboard/sandboxes/sandbox-1/fragment"`,
		}},
		{path: "/dashboard/dashboard/sandboxes/sandbox-1/fragment", status: http.StatusOK, contains: []string{
			`hx-get="/dashboard/dashboard/sandboxes/sandbox-1/fragment"`,
		}},
	}

	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			response := httptest.NewRecorder()
			app.routes().ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, test.status, response.Body.String())
			}
			if test.location != "" && response.Header().Get("Location") != test.location {
				t.Errorf("Location = %q, want %q", response.Header().Get("Location"), test.location)
			}
			for _, expected := range test.contains {
				if !strings.Contains(response.Body.String(), expected) {
					t.Errorf("response does not contain %q: %s", expected, response.Body.String())
				}
			}
		})
	}
}

func TestRoutes(t *testing.T) {
	service := &fakeSandboxService{}
	app, err := newApplication(
		"/tmp/test-kubeconfig",
		service,
		service,
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
			name:        "snapshots page",
			method:      http.MethodGet,
			path:        "/snapshots",
			contentType: "text/html; charset=utf-8",
			contains:    "hx-get=\"/dashboard/snapshots\"",
		},
		{
			name:        "snapshots fragment",
			method:      http.MethodGet,
			path:        "/dashboard/snapshots",
			contentType: "text/html; charset=utf-8",
			contains:    "No snapshots yet",
		},
		{
			name:        "create sandbox",
			method:      http.MethodPost,
			path:        "/dashboard/sandboxes",
			contentType: "text/html; charset=utf-8",
			contains:    "sandbox-created",
		},
		{
			name:           "sandbox detail page",
			method:         http.MethodGet,
			path:           "/dashboard/sandboxes/sandbox-created",
			contentType:    "text/html; charset=utf-8",
			contains:       "details-pane-toggle",
			doesNotContain: `id="deploy-sandbox-button"`,
		},
		{
			name:        "sandbox detail fragment",
			method:      http.MethodGet,
			path:        "/dashboard/sandboxes/sandbox-created/fragment",
			contentType: "text/html; charset=utf-8",
			contains:    "sandbox-terminal-stage",
		},
		{
			name:        "pause sandbox",
			method:      http.MethodPost,
			path:        "/dashboard/sandboxes/sandbox-created/pause",
			contentType: "text/html; charset=utf-8",
			contains:    "Pausing",
		},
		{
			name:        "resume sandbox",
			method:      http.MethodPost,
			path:        "/dashboard/sandboxes/sandbox-created/resume",
			contentType: "text/html; charset=utf-8",
			contains:    "Resuming",
		},
		{
			name:        "sandbox live stats",
			method:      http.MethodGet,
			path:        "/dashboard/sandboxes/sandbox-created/stats",
			contentType: "text/html; charset=utf-8",
			contains:    "12.5%",
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
			if test.path == "/dashboard/sandboxes" {
				app.background.Wait()
			}

			result := response.Result()
			defer result.Body.Close()

			if result.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want %d", result.StatusCode, http.StatusOK)
			}
			if test.path == "/dashboard/sandboxes" {
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
