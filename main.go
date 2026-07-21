package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bahe-msft/osb-dashboard/internal/opensandbox"
	"github.com/coder/websocket"
)

const (
	defaultHTTPAddr = "127.0.0.1:8080"
	statsSampleUS   = 250_000
)

const terminalShellProbeCommand = `command -v bash >/dev/null 2>&1`

const sandboxStatsCommand = `set -eu
read_cpu_usage_v2() {
  while read -r key value _; do
    if [ "$key" = usage_usec ]; then
      printf '%s\n' "$value"
      return 0
    fi
  done < /sys/fs/cgroup/cpu.stat
  return 1
}
if [ -r /sys/fs/cgroup/cpu.stat ] && [ -r /sys/fs/cgroup/cpu.max ]; then
  cpu_unit=us
  cpu_start=$(read_cpu_usage_v2)
  read -r cpu_quota cpu_period < /sys/fs/cgroup/cpu.max
elif [ -r /sys/fs/cgroup/cpuacct/cpuacct.usage ] && [ -r /sys/fs/cgroup/cpu/cpu.cfs_quota_us ]; then
  cpu_unit=ns
  cpu_start=$(cat /sys/fs/cgroup/cpuacct/cpuacct.usage)
  cpu_quota=$(cat /sys/fs/cgroup/cpu/cpu.cfs_quota_us)
  cpu_period=$(cat /sys/fs/cgroup/cpu/cpu.cfs_period_us)
else
  printf 'unsupported CPU cgroup layout\n' >&2
  exit 2
fi
sleep 0.25
if [ "$cpu_unit" = us ]; then
  cpu_end=$(read_cpu_usage_v2)
else
  cpu_end=$(cat /sys/fs/cgroup/cpuacct/cpuacct.usage)
fi
cpu_count=0
for cpu_path in /sys/devices/system/cpu/cpu[0-9]*; do
  if [ -d "$cpu_path" ]; then cpu_count=$((cpu_count + 1)); fi
done
if [ "$cpu_count" -eq 0 ]; then cpu_count=1; fi
read -r load_1 _ < /proc/loadavg
if [ -r /sys/fs/cgroup/memory.current ] && [ -r /sys/fs/cgroup/memory.max ]; then
  memory_current=$(cat /sys/fs/cgroup/memory.current)
  memory_max=$(cat /sys/fs/cgroup/memory.max)
elif [ -r /sys/fs/cgroup/memory/memory.usage_in_bytes ] && [ -r /sys/fs/cgroup/memory/memory.limit_in_bytes ]; then
  memory_current=$(cat /sys/fs/cgroup/memory/memory.usage_in_bytes)
  memory_max=$(cat /sys/fs/cgroup/memory/memory.limit_in_bytes)
else
  printf 'unsupported memory cgroup layout\n' >&2
  exit 2
fi
printf 'cpu_unit=%s\ncpu_start=%s\ncpu_end=%s\ncpu_quota=%s\ncpu_period=%s\ncpu_count=%s\nload_1=%s\nmemory_current=%s\nmemory_max=%s\n' \
  "$cpu_unit" "$cpu_start" "$cpu_end" "$cpu_quota" "$cpu_period" "$cpu_count" "$load_1" "$memory_current" "$memory_max"`

//go:generate ./scripts/fetch-ghostty-web.sh
//go:generate ./scripts/fetch-ui-assets.sh

//go:embed web
var webFiles embed.FS

type application struct {
	assets                   http.Handler
	kubeconfigPath           string
	indexTemplate            *template.Template
	overviewTemplate         *template.Template
	sandboxTemplate          *template.Template
	snapshotsTemplate        *template.Template
	snapshotDetailTemplate   *template.Template
	snapshotResultTemplate   *template.Template
	deploymentResultTemplate *template.Template
	clusterStatsTemplate     *template.Template
	statsTemplate            *template.Template
	sandboxReader            opensandbox.Reader
	sandboxWriter            opensandbox.Writer
	sandboxTerminal          opensandbox.Terminal
	sandboxCommands          opensandbox.CommandRunner
	sandboxImage             string
	context                  context.Context
	background               sync.WaitGroup
	statsMutex               sync.Mutex
	statsCache               map[string]cachedSandboxStats
	sandboxCacheMutex        sync.Mutex
	sandboxCache             []opensandbox.Sandbox
	sandboxCacheErr          error
	sandboxCacheUntil        time.Time
	snapshotCacheMutex       sync.Mutex
	snapshotCache            []opensandbox.Snapshot
	snapshotCacheErr         error
	snapshotCacheUntil       time.Time
	basePath                 string
}

type commandConfig struct {
	kubeconfigPath       string
	openSandboxNamespace string
	sandboxNamespace     string
	sandboxImage         string
	authToken            string
	basePath             string
}

type sandboxView struct {
	ID                string
	Name              string
	State             string
	CreatedAtISO      string
	CreatedAtFallback string
	Namespace         string
	PodName           string
	Image             string
	Resources         string
}

type sandboxStateCount struct {
	State string
	Count int
}

type sandboxGroup struct {
	State     string
	Label     string
	Sandboxes []sandboxView
}

type overviewData struct {
	Total         int
	SnapshotTotal int
	StateCounts   []sandboxStateCount
	Groups        []sandboxGroup
	Error         string
}

type pageData struct {
	SandboxImage string
	SandboxID    string
	SnapshotID   string
	Page         string
	ContentURL   string
	BasePath     string
}

type detailItem struct {
	Label string
	Value string
}

type sandboxDetailData struct {
	ID                string
	Total             int
	SnapshotTotal     int
	State             string
	StateLabel        string
	CreatedAtISO      string
	CreatedAtFallback string
	Namespace         string
	PodName           string
	Image             string
	Resources         string
	Sources           string
	LifecycleManaged  bool
	Metadata          []detailItem
	Error             string
}

type clusterStatsNodeView struct {
	Name           string
	SandboxCount   int
	CPUReserved    string
	CPUPercent     float64
	MemoryReserved string
	MemoryPercent  float64
}

type clusterStatsData struct {
	SandboxTotal     int
	SnapshotTotal    int
	ScheduledTotal   int
	NodeTotal        int
	SandboxesPerNode string
	Nodes            []clusterStatsNodeView
	Error            string
}

type sandboxStatsData struct {
	SandboxID     string
	CPU           string
	CPUPercent    float64
	CPULevel      string
	Load          string
	LoadPercent   float64
	LoadLevel     string
	Memory        string
	MemoryPercent float64
	MemoryLevel   string
	MemoryLimited bool
	Error         string
}

type snapshotView struct {
	ID                       string
	SandboxID                string
	Name                     string
	HasName                  bool
	State                    string
	StateLabel               string
	Reason                   string
	Message                  string
	CreatedAtISO             string
	CreatedAtFallback        string
	LastTransitionAtISO      string
	LastTransitionAtFallback string
	SourceSandboxAvailable   bool
	CanRestore               bool
	CanDelete                bool
}

type snapshotStateCount struct {
	State string
	Count int
}

type snapshotGroup struct {
	State     string
	Label     string
	Snapshots []snapshotView
}

type snapshotsData struct {
	Total        int
	SandboxTotal int
	StateCounts  []snapshotStateCount
	Groups       []snapshotGroup
	Error        string
}

type snapshotCreateResultData struct {
	ID       string
	Name     string
	State    string
	StateKey string
	Reason   string
	Message  string
	Total    int
	Polling  bool
	Ready    bool
	Failed   bool
}

type snapshotDetailData struct {
	snapshotView
	Total        int
	SandboxTotal int
	Error        string
}

type sandboxDeploymentResultData struct {
	SandboxID    string
	SnapshotID   string
	SnapshotName string
	State        string
	StateKey     string
	Message      string
	SandboxTotal int
	Polling      bool
	Ready        bool
	Failed       bool
}

type cachedSandboxStats struct {
	data      sandboxStatsData
	expiresAt time.Time
}

type sandboxUsageStats struct {
	CPUPercent    float64
	CPUCapacity   float64
	Load1         float64
	MemoryCurrent uint64
	MemoryMax     uint64
	MemoryLimited bool
}

var defaultSandboxStates = []string{"running", "pending", "paused", "failed"}
var defaultSnapshotStates = []string{"creating", "ready", "failed", "deleting"}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(logger)

	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		logger.Error("dashboard exited", slog.Any("error", err))
		os.Exit(1)
	}
}

func run(args []string) error {
	config, err := parseCommandConfig(args)
	if err != nil {
		return err
	}
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = defaultHTTPAddr
	}
	if config.authToken == "" && !isLoopbackAddress(addr) {
		return errors.New("--auth-token is required when HTTP_ADDR is not loopback")
	}

	client, err := opensandbox.NewFromKubeconfig(config.kubeconfigPath, opensandbox.Options{
		Namespace:         config.openSandboxNamespace,
		WorkloadNamespace: config.sandboxNamespace,
		Logger:            slog.Default(),
	})
	if err != nil {
		return fmt.Errorf("create OpenSandbox client: %w", err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			slog.Error("close OpenSandbox client", slog.Any("error", err))
		}
	}()

	appContext, cancelApp := context.WithCancel(context.Background())
	app, err := newApplication(config.kubeconfigPath, client, client, client, client, config.sandboxImage, appContext, config.basePath)
	if err != nil {
		cancelApp()
		return err
	}

	handler := csrfProtection(app.routes())
	handler = tokenAuthentication(config.authToken, handler)
	handler = securityHeaders(handler)
	server := &http.Server{
		Addr:              addr,
		Handler:           requestLogger(slog.Default(), handler),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	shutdownSignal, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	go func() {
		<-shutdownSignal.Done()

		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := server.Shutdown(shutdownContext); err != nil {
			slog.Error("graceful shutdown", slog.Any("error", err))
		}
	}()

	slog.Info(
		"dashboard listening",
		slog.String("address", addr),
		slog.String("kubeconfig", app.kubeconfigPath),
		slog.String("base_path", app.basePath),
	)
	serveErr := server.ListenAndServe()
	cancelApp()
	app.background.Wait()
	if !errors.Is(serveErr, http.ErrServerClosed) {
		return fmt.Errorf("serve dashboard: %w", serveErr)
	}

	return nil
}

func parseCommandConfig(args []string) (commandConfig, error) {
	var config commandConfig

	flags := flag.NewFlagSet("osb-dashboard", flag.ContinueOnError)
	flags.StringVar(
		&config.kubeconfigPath,
		"kubeconfig",
		"",
		"path to the kubeconfig for a cluster with OpenSandbox deployed",
	)
	flags.StringVar(
		&config.openSandboxNamespace,
		"opensandbox-namespace",
		"opensandbox-system",
		"namespace containing the OpenSandbox lifecycle service",
	)
	flags.StringVar(
		&config.sandboxNamespace,
		"sandbox-namespace",
		"opensandbox",
		"namespace containing sandbox custom resources",
	)
	flags.StringVar(
		&config.sandboxImage,
		"sandbox-image",
		"python:3.12-slim",
		"container image used by the dashboard's create action",
	)
	flags.StringVar(
		&config.authToken,
		"auth-token",
		os.Getenv("OSB_DASHBOARD_AUTH_TOKEN"),
		"token required for dashboard access (required for non-loopback HTTP_ADDR)",
	)
	flags.StringVar(
		&config.basePath,
		"base-path",
		os.Getenv("OSB_DASHBOARD_BASE_PATH"),
		"URL path prefix used to serve the dashboard (for example /dashboard)",
	)

	if err := flags.Parse(args); err != nil {
		return commandConfig{}, err
	}
	if flags.NArg() != 0 {
		return commandConfig{}, fmt.Errorf("unexpected positional arguments: %v", flags.Args())
	}
	if config.kubeconfigPath == "" {
		return commandConfig{}, errors.New("--kubeconfig is required")
	}
	if strings.TrimSpace(config.openSandboxNamespace) == "" {
		return commandConfig{}, errors.New("--opensandbox-namespace is required")
	}
	if strings.TrimSpace(config.sandboxNamespace) == "" {
		return commandConfig{}, errors.New("--sandbox-namespace is required")
	}
	if strings.TrimSpace(config.sandboxImage) == "" {
		return commandConfig{}, errors.New("--sandbox-image is required")
	}
	basePath, err := normalizeBasePath(config.basePath)
	if err != nil {
		return commandConfig{}, err
	}
	config.basePath = basePath

	resolvedPath, err := filepath.Abs(config.kubeconfigPath)
	if err != nil {
		return commandConfig{}, fmt.Errorf("resolve kubeconfig path: %w", err)
	}

	kubeconfig, err := os.Open(resolvedPath)
	if err != nil {
		return commandConfig{}, fmt.Errorf("open kubeconfig %q: %w", resolvedPath, err)
	}
	if err := kubeconfig.Close(); err != nil {
		return commandConfig{}, fmt.Errorf("close kubeconfig %q: %w", resolvedPath, err)
	}

	config.kubeconfigPath = resolvedPath
	return config, nil
}

func normalizeBasePath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "/" {
		return "", nil
	}
	if strings.ContainsAny(value, "?#") {
		return "", errors.New("--base-path must be a URL path without a query or fragment")
	}
	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == ".." {
			return "", errors.New("--base-path must not contain parent path segments")
		}
	}
	cleaned := pathpkg.Clean(value)
	if cleaned == "." || cleaned == "/" {
		return "", nil
	}
	return strings.TrimRight(cleaned, "/"), nil
}

func newApplication(
	kubeconfigPath string,
	sandboxReader opensandbox.Reader,
	sandboxWriter opensandbox.Writer,
	sandboxTerminal opensandbox.Terminal,
	sandboxCommands opensandbox.CommandRunner,
	sandboxImage string,
	appContext context.Context,
	basePaths ...string,
) (*application, error) {
	basePath := ""
	if len(basePaths) != 0 {
		basePath = basePaths[0]
	}
	assetsFS, err := fs.Sub(webFiles, "web/assets")
	if err != nil {
		return nil, fmt.Errorf("load web assets: %w", err)
	}

	indexTemplate, err := template.ParseFS(webFiles, "web/index.html")
	if err != nil {
		return nil, fmt.Errorf("parse index template: %w", err)
	}

	overviewTemplate, err := template.ParseFS(webFiles, "web/overview.html")
	if err != nil {
		return nil, fmt.Errorf("parse overview template: %w", err)
	}

	sandboxTemplate, err := template.ParseFS(webFiles, "web/sandbox.html")
	if err != nil {
		return nil, fmt.Errorf("parse sandbox template: %w", err)
	}

	snapshotsTemplate, err := template.ParseFS(webFiles, "web/snapshots.html")
	if err != nil {
		return nil, fmt.Errorf("parse snapshots template: %w", err)
	}

	snapshotDetailTemplate, err := template.ParseFS(webFiles, "web/snapshot.html")
	if err != nil {
		return nil, fmt.Errorf("parse snapshot detail template: %w", err)
	}

	snapshotResultTemplate, err := template.ParseFS(webFiles, "web/snapshot-result.html")
	if err != nil {
		return nil, fmt.Errorf("parse snapshot result template: %w", err)
	}

	deploymentResultTemplate, err := template.ParseFS(webFiles, "web/deployment-result.html")
	if err != nil {
		return nil, fmt.Errorf("parse deployment result template: %w", err)
	}

	clusterStatsTemplate, err := template.ParseFS(webFiles, "web/cluster-stats.html")
	if err != nil {
		return nil, fmt.Errorf("parse cluster stats template: %w", err)
	}

	statsTemplate, err := template.ParseFS(webFiles, "web/stats.html")
	if err != nil {
		return nil, fmt.Errorf("parse stats template: %w", err)
	}

	return &application{
		assets:                   http.FileServer(http.FS(assetsFS)),
		kubeconfigPath:           kubeconfigPath,
		indexTemplate:            indexTemplate,
		overviewTemplate:         overviewTemplate,
		sandboxTemplate:          sandboxTemplate,
		snapshotsTemplate:        snapshotsTemplate,
		snapshotDetailTemplate:   snapshotDetailTemplate,
		snapshotResultTemplate:   snapshotResultTemplate,
		deploymentResultTemplate: deploymentResultTemplate,
		clusterStatsTemplate:     clusterStatsTemplate,
		statsTemplate:            statsTemplate,
		sandboxReader:            sandboxReader,
		sandboxWriter:            sandboxWriter,
		sandboxTerminal:          sandboxTerminal,
		sandboxCommands:          sandboxCommands,
		sandboxImage:             sandboxImage,
		basePath:                 basePath,
		context:                  appContext,
		statsCache:               make(map[string]cachedSandboxStats),
	}, nil
}

func (app *application) executeHTMLTemplate(w http.ResponseWriter, tmpl *template.Template, name string, data any) error {
	var rendered bytes.Buffer
	var err error
	if name == "" {
		err = tmpl.Execute(&rendered, data)
	} else {
		err = tmpl.ExecuteTemplate(&rendered, name, data)
	}
	if err != nil {
		return err
	}
	content := rendered.String()
	if app.basePath != "" {
		attributes := []string{"href", "src", "action", "hx-get", "hx-post", "hx-delete", "hx-push-url"}
		for _, attribute := range attributes {
			content = strings.ReplaceAll(content, attribute+`="/`, attribute+`="`+app.basePath+`/`)
		}
		content = strings.ReplaceAll(
			content,
			`href="`+app.basePath+`/dashboard/sandboxes/`,
			`href="`+app.basePath+`/sandboxes/`,
		)
		content = strings.ReplaceAll(
			content,
			`hx-push-url="`+app.basePath+`/dashboard/sandboxes/`,
			`hx-push-url="`+app.basePath+`/sandboxes/`,
		)
	}
	_, err = w.Write([]byte(content))
	return err
}

func (app *application) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", app.index)
	mux.HandleFunc("GET /snapshots", app.snapshotsPage)
	mux.HandleFunc("GET /snapshots/{id}", app.snapshotPage)
	mux.HandleFunc("GET /stats", app.clusterStatsPage)
	mux.HandleFunc("GET /sandboxes/{id}", app.sandboxPage)
	mux.HandleFunc("GET /dashboard/overview", app.overview)
	mux.HandleFunc("GET /dashboard/stats", app.clusterStats)
	mux.HandleFunc("GET /dashboard/snapshots", app.snapshots)
	mux.HandleFunc("GET /dashboard/snapshots/{id}/fragment", app.snapshotDetail)
	mux.HandleFunc("GET /dashboard/snapshots/{id}/status", app.snapshotCreateStatus)
	mux.HandleFunc("POST /dashboard/snapshots", app.createSnapshot)
	mux.HandleFunc("DELETE /dashboard/snapshots/{id}", app.deleteSnapshot)
	mux.HandleFunc("GET /dashboard/sandboxes/{id}", app.sandboxPage)
	mux.HandleFunc("GET /dashboard/sandboxes/{id}/fragment", app.sandboxDetail)
	mux.HandleFunc("GET /dashboard/sandboxes/{id}/stats", app.sandboxStats)
	mux.HandleFunc("GET /dashboard/sandboxes/{id}/deployment-status", app.sandboxDeploymentStatus)
	mux.HandleFunc("GET /dashboard/sandboxes/{id}/terminal/pty", app.sandboxPTY)
	mux.HandleFunc("POST /dashboard/sandboxes/{id}/pause", app.pauseSandbox)
	mux.HandleFunc("POST /dashboard/sandboxes/{id}/resume", app.resumeSandbox)
	mux.HandleFunc("POST /dashboard/sandboxes", app.createSandbox)
	mux.HandleFunc("DELETE /dashboard/sandboxes/{id}", app.deleteSandbox)
	mux.HandleFunc("GET /healthz", health)
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", app.assets))

	if app.basePath == "" {
		return mux
	}
	root := http.NewServeMux()
	root.HandleFunc("GET "+app.basePath, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, app.basePath+"/", http.StatusPermanentRedirect)
	})
	root.Handle(app.basePath+"/", http.StripPrefix(app.basePath, mux))
	return root
}

func (app *application) index(w http.ResponseWriter, r *http.Request) {
	app.renderPage(w, r, "list", "", "")
}

func (app *application) snapshotsPage(w http.ResponseWriter, r *http.Request) {
	app.renderPage(w, r, "snapshots", "", "")
}

func (app *application) clusterStatsPage(w http.ResponseWriter, r *http.Request) {
	app.renderPage(w, r, "stats", "", "")
}

func (app *application) snapshotPage(w http.ResponseWriter, r *http.Request) {
	app.renderPage(w, r, "snapshot-detail", "", r.PathValue("id"))
}

func (app *application) sandboxPage(w http.ResponseWriter, r *http.Request) {
	app.renderPage(w, r, "detail", r.PathValue("id"), "")
}

func (app *application) renderPage(w http.ResponseWriter, r *http.Request, page, sandboxID, snapshotID string) {
	data := pageData{
		SandboxImage: app.sandboxImage,
		SandboxID:    sandboxID,
		SnapshotID:   snapshotID,
		Page:         page,
		ContentURL:   "/dashboard/overview",
		BasePath:     app.basePath,
	}
	if page == "snapshots" {
		data.ContentURL = "/dashboard/snapshots"
	} else if page == "stats" {
		data.ContentURL = "/dashboard/stats"
	} else if snapshotID != "" {
		data.ContentURL = "/dashboard/snapshots/" + url.PathEscape(snapshotID) + "/fragment"
	} else if sandboxID != "" {
		data.ContentURL = "/dashboard/sandboxes/" + url.PathEscape(sandboxID) + "/fragment"
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := app.executeHTMLTemplate(w, app.indexTemplate, "", data); err != nil {
		slog.ErrorContext(r.Context(), "render index", slog.Any("error", err))
	}
}

func (app *application) overview(w http.ResponseWriter, r *http.Request) {
	app.renderOverview(w, app.loadOverviewData(r.Context()))
}

func (app *application) snapshots(w http.ResponseWriter, r *http.Request) {
	app.renderSnapshots(w, r, app.loadSnapshotsData(r.Context(), false))
}

func (app *application) clusterStats(w http.ResponseWriter, r *http.Request) {
	sandboxes, sandboxErr := app.listSandboxes(r.Context(), false)
	snapshots, _ := app.listSnapshots(r.Context(), false)
	loads, loadErr := app.sandboxReader.ListSandboxNodeLoads(r.Context())
	data := clusterStatsData{
		SandboxTotal:  len(sandboxes),
		SnapshotTotal: len(snapshots),
		NodeTotal:     len(loads),
	}
	for _, load := range loads {
		data.ScheduledTotal += load.SandboxCount
		cpuPercent := resourcePercent(load.CPURequestedMilli, load.CPUAllocatableMilli)
		memoryPercent := resourcePercent(load.MemoryRequestedBytes, load.MemoryAllocatableBytes)
		data.Nodes = append(data.Nodes, clusterStatsNodeView{
			Name:           load.Name,
			SandboxCount:   load.SandboxCount,
			CPUReserved:    formatCPUReservation(load.CPURequestedMilli, load.CPUAllocatableMilli),
			CPUPercent:     cpuPercent,
			MemoryReserved: formatMemoryReservation(load.MemoryRequestedBytes, load.MemoryAllocatableBytes),
			MemoryPercent:  memoryPercent,
		})
	}
	if data.NodeTotal > 0 {
		data.SandboxesPerNode = strconv.FormatFloat(float64(data.ScheduledTotal)/float64(data.NodeTotal), 'f', 1, 64)
	} else {
		data.SandboxesPerNode = "—"
	}
	if err := errors.Join(sandboxErr, loadErr); err != nil {
		data.Error = "Some cluster statistics could not be loaded: " + err.Error()
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := app.executeHTMLTemplate(w, app.clusterStatsTemplate, "", data); err != nil {
		slog.ErrorContext(r.Context(), "render cluster stats", slog.Any("error", err))
	}
}

func resourcePercent(used, total int64) float64 {
	if used <= 0 || total <= 0 {
		return 0
	}
	return float64(used) / float64(total) * 100
}

func formatCPUReservation(requested, allocatable int64) string {
	if allocatable <= 0 {
		return formatCPUCapacity(float64(requested) / 1000)
	}
	return formatCPUCapacity(float64(requested)/1000) + " / " + formatCPUCapacity(float64(allocatable)/1000)
}

func formatMemoryReservation(requested, allocatable int64) string {
	if allocatable <= 0 {
		return formatByteCount(uint64(max(requested, 0)))
	}
	return formatByteCount(uint64(max(requested, 0))) + " / " + formatByteCount(uint64(allocatable))
}

func (app *application) snapshotDetail(w http.ResponseWriter, r *http.Request) {
	snapshotID := r.PathValue("id")
	snapshot, getErr := app.sandboxReader.GetSnapshot(r.Context(), snapshotID)
	snapshots, _ := app.listSnapshots(r.Context(), false)
	sandboxes, _ := app.listSandboxes(r.Context(), false)
	data := snapshotDetailData{Total: len(snapshots), SandboxTotal: len(sandboxes)}
	if getErr != nil {
		data.ID = snapshotID
		data.Name = snapshotID
		data.Error = "Unable to load snapshot: " + getErr.Error()
	} else {
		sourceAvailable := false
		for _, sandbox := range sandboxes {
			if sandbox.ID == snapshot.SandboxID {
				sourceAvailable = true
				break
			}
		}
		data.snapshotView = snapshotToView(snapshot, sourceAvailable)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := app.executeHTMLTemplate(w, app.snapshotDetailTemplate, "", data); err != nil {
		slog.ErrorContext(r.Context(), "render snapshot detail", slog.String("snapshot_id", snapshotID), slog.Any("error", err))
	}
}

func (app *application) snapshotCreateStatus(w http.ResponseWriter, r *http.Request) {
	snapshotID := r.PathValue("id")
	snapshot, err := app.sandboxReader.GetSnapshot(r.Context(), snapshotID)
	var data snapshotCreateResultData
	if err != nil {
		data = snapshotCreateResultData{
			ID:       snapshotID,
			Name:     snapshotID,
			State:    "Checking",
			StateKey: "checking",
			Message:  "Snapshot status is temporarily unavailable. Retrying…",
			Polling:  true,
		}
	} else {
		data = snapshotCreateResultFromSnapshot(snapshot, 0)
		app.invalidateSnapshotCache()
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := app.executeHTMLTemplate(w, app.snapshotResultTemplate, "snapshot-result-status", data); err != nil {
		slog.ErrorContext(r.Context(), "render snapshot creation status", slog.String("snapshot_id", snapshotID), slog.Any("error", err))
	}
}

func snapshotCreateResultFromSnapshot(snapshot opensandbox.Snapshot, total int) snapshotCreateResultData {
	stateKey := normalizeSandboxState(snapshot.State)
	name := snapshot.Name
	if name == "" {
		name = snapshot.ID
	}
	return snapshotCreateResultData{
		ID:       snapshot.ID,
		Name:     name,
		State:    sandboxStateLabel(stateKey),
		StateKey: stateKey,
		Reason:   snapshot.Reason,
		Message:  snapshot.Message,
		Total:    total,
		Polling:  stateKey == "creating" || stateKey == "checking",
		Ready:    stateKey == "ready",
		Failed:   stateKey == "failed",
	}
}

func (app *application) createSnapshot(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Unable to read snapshot form", http.StatusBadRequest)
		return
	}
	sandboxID := strings.TrimSpace(r.FormValue("sandboxID"))
	if sandboxID == "" {
		http.Error(w, "Select a sandbox to snapshot", http.StatusBadRequest)
		return
	}

	sandboxes, listErr := app.listSandboxes(r.Context(), true)
	var target *opensandbox.Sandbox
	for index := range sandboxes {
		if sandboxes[index].ID == sandboxID {
			target = &sandboxes[index]
			break
		}
	}
	if target == nil {
		message := "Sandbox was not found"
		if listErr != nil {
			message = "Unable to locate sandbox: " + listErr.Error()
		}
		http.Error(w, message, http.StatusNotFound)
		return
	}
	if normalizeSandboxState(target.State) != "running" || !sandboxHasSource(*target, opensandbox.SourceLifecycle) {
		http.Error(w, "Snapshots require a running sandbox managed by the Lifecycle API", http.StatusConflict)
		return
	}

	created, err := app.sandboxWriter.CreateSnapshot(r.Context(), sandboxID, strings.TrimSpace(r.FormValue("name")))
	if err != nil {
		slog.ErrorContext(r.Context(), "create snapshot", slog.String("sandbox_id", sandboxID), slog.Any("error", err))
		http.Error(w, "Unable to create snapshot: "+err.Error(), http.StatusBadGateway)
		return
	}

	app.invalidateSnapshotCache()
	snapshots, _ := app.listSnapshots(r.Context(), true)
	data := snapshotCreateResultFromSnapshot(created, len(snapshots))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("HX-Trigger", "snapshotCreated")
	if err := app.executeHTMLTemplate(w, app.snapshotResultTemplate, "snapshot-result", data); err != nil {
		slog.ErrorContext(r.Context(), "render snapshot result", slog.String("snapshot_id", created.ID), slog.Any("error", err))
	}
}

func (app *application) deleteSnapshot(w http.ResponseWriter, r *http.Request) {
	snapshotID := r.PathValue("id")
	snapshots, listErr := app.listSnapshots(r.Context(), true)
	var target *opensandbox.Snapshot
	for index := range snapshots {
		if snapshots[index].ID == snapshotID {
			target = &snapshots[index]
			break
		}
	}
	if target == nil {
		data := app.snapshotsDataFromSnapshots(r.Context(), snapshots)
		if listErr != nil {
			data.Error = "Unable to locate snapshot: " + listErr.Error()
		} else {
			data.Error = fmt.Sprintf("Snapshot %q was not found", snapshotID)
		}
		app.renderSnapshots(w, r, data)
		return
	}
	state := normalizeSandboxState(target.State)
	if state == "creating" || state == "deleting" {
		data := app.snapshotsDataFromSnapshots(r.Context(), snapshots)
		data.Error = fmt.Sprintf("Snapshot cannot be deleted while it is %s", state)
		app.renderSnapshots(w, r, data)
		return
	}
	if err := app.sandboxWriter.DeleteSnapshot(r.Context(), snapshotID); err != nil {
		slog.ErrorContext(r.Context(), "delete snapshot", slog.String("snapshot_id", snapshotID), slog.Any("error", err))
		data := app.snapshotsDataFromSnapshots(r.Context(), snapshots)
		data.Error = "Unable to delete snapshot: " + err.Error()
		app.renderSnapshots(w, r, data)
		return
	}

	app.invalidateSnapshotCache()
	app.renderSnapshots(w, r, app.loadSnapshotsData(r.Context(), true))
}

func (app *application) sandboxDetail(w http.ResponseWriter, r *http.Request) {
	app.renderSandboxDetail(w, r, app.loadSandboxDetailData(r.Context(), r.PathValue("id"), false))
}

func (app *application) loadSandboxDetailData(ctx context.Context, sandboxID string, fresh bool) sandboxDetailData {
	sandboxes, listErr := app.listSandboxes(ctx, fresh)
	data := sandboxDetailData{ID: sandboxID, Total: len(sandboxes)}
	for _, sandbox := range sandboxes {
		if sandbox.ID == sandboxID {
			data = sandboxDetailFromSandbox(sandbox)
			data.Total = len(sandboxes)
			break
		}
	}
	if data.State == "" {
		data.Error = fmt.Sprintf("Sandbox %q was not found", sandboxID)
	} else if listErr != nil {
		data.Error = "Some sandbox sources could not be loaded: " + listErr.Error()
	}
	return data
}

func (app *application) renderSandboxDetail(w http.ResponseWriter, r *http.Request, data sandboxDetailData) {
	snapshots, _ := app.listSnapshots(r.Context(), false)
	data.SnapshotTotal = len(snapshots)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := app.executeHTMLTemplate(w, app.sandboxTemplate, "", data); err != nil {
		slog.ErrorContext(r.Context(), "render sandbox detail", slog.String("sandbox_id", data.ID), slog.Any("error", err))
	}
}

func (app *application) pauseSandbox(w http.ResponseWriter, r *http.Request) {
	app.changeSandboxState(w, r, "pause", "running", "paused")
}

func (app *application) resumeSandbox(w http.ResponseWriter, r *http.Request) {
	app.changeSandboxState(w, r, "resume", "paused", "running")
}

func (app *application) changeSandboxState(w http.ResponseWriter, r *http.Request, action, requiredState, expectedState string) {
	sandboxID := r.PathValue("id")
	sandboxes, listErr := app.listSandboxes(r.Context(), true)
	var target *opensandbox.Sandbox
	for index := range sandboxes {
		if sandboxes[index].ID == sandboxID {
			target = &sandboxes[index]
			break
		}
	}
	if target == nil {
		data := app.loadSandboxDetailData(r.Context(), sandboxID, true)
		if listErr != nil {
			data.Error = "Unable to locate sandbox: " + listErr.Error()
		}
		app.renderSandboxDetail(w, r, data)
		return
	}
	state := normalizeSandboxState(target.State)
	if state == expectedState {
		data := sandboxDetailFromSandbox(*target)
		data.Total = len(sandboxes)
		app.renderSandboxDetail(w, r, data)
		return
	}
	if state != requiredState {
		data := sandboxDetailFromSandbox(*target)
		data.Total = len(sandboxes)
		data.Error = fmt.Sprintf("Sandbox must be %s before it can %s", requiredState, action)
		app.renderSandboxDetail(w, r, data)
		return
	}
	if !sandboxHasSource(*target, opensandbox.SourceLifecycle) {
		data := sandboxDetailFromSandbox(*target)
		data.Total = len(sandboxes)
		data.Error = "Pause and resume require a sandbox managed by the Lifecycle API"
		app.renderSandboxDetail(w, r, data)
		return
	}

	var err error
	if action == "pause" {
		err = app.sandboxWriter.PauseSandbox(r.Context(), sandboxID)
	} else {
		err = app.sandboxWriter.ResumeSandbox(r.Context(), sandboxID)
	}
	if err != nil {
		slog.ErrorContext(r.Context(), action+" sandbox", slog.String("sandbox_id", sandboxID), slog.Any("error", err))
		data := sandboxDetailFromSandbox(*target)
		data.Total = len(sandboxes)
		data.Error = fmt.Sprintf("Unable to %s sandbox: %v", action, err)
		app.renderSandboxDetail(w, r, data)
		return
	}

	app.invalidateSandboxCache()
	app.statsMutex.Lock()
	delete(app.statsCache, sandboxID)
	app.statsMutex.Unlock()
	data := sandboxDetailFromSandbox(*target)
	data.Total = len(sandboxes)
	if action == "pause" {
		data.State = "pausing"
	} else {
		data.State = "resuming"
	}
	data.StateLabel = sandboxStateLabel(data.State)
	w.Header().Set("HX-Trigger", "sandboxStateChanged")
	app.renderSandboxDetail(w, r, data)
}

func (app *application) sandboxStats(w http.ResponseWriter, r *http.Request) {
	sandboxID := r.PathValue("id")
	if data, ok := app.cachedSandboxStats(sandboxID); ok {
		app.renderSandboxStats(w, r, data)
		return
	}
	data := sandboxStatsData{SandboxID: sandboxID, CPU: "—", Memory: "—"}

	statsContext, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	result, err := app.sandboxCommands.RunCommand(statsContext, sandboxID, sandboxStatsCommand)
	if err == nil {
		var usage sandboxUsageStats
		usage, err = parseSandboxStats(result)
		if err == nil {
			data.CPUPercent = usage.CPUPercent
			data.CPU = fmt.Sprintf("%.1f%%", usage.CPUPercent)
			data.CPULevel = utilizationLevel(usage.CPUPercent)
			data.LoadPercent = usage.Load1 / usage.CPUCapacity * 100
			if data.LoadPercent > 100 {
				data.LoadPercent = 100
			}
			data.Load = fmt.Sprintf("%.2f / %s", usage.Load1, formatCPUCapacity(usage.CPUCapacity))
			data.LoadLevel = utilizationLevel(data.LoadPercent)
			data.MemoryLimited = usage.MemoryLimited
			data.Memory = formatByteCount(usage.MemoryCurrent) + " / unlimited"
			if usage.MemoryLimited {
				data.MemoryPercent = float64(usage.MemoryCurrent) / float64(usage.MemoryMax) * 100
				if data.MemoryPercent > 100 {
					data.MemoryPercent = 100
				}
				data.MemoryLevel = utilizationLevel(data.MemoryPercent)
				data.Memory = formatByteCount(usage.MemoryCurrent) + " / " + formatByteCount(usage.MemoryMax)
			}
		}
	}
	cacheDuration := 4 * time.Second
	if err != nil {
		slog.ErrorContext(r.Context(), "load sandbox stats", slog.String("sandbox_id", sandboxID), slog.Any("error", err))
		data.Error = "Unable to load live usage"
		cacheDuration = time.Second
	}
	app.cacheSandboxStats(sandboxID, data, cacheDuration)
	app.renderSandboxStats(w, r, data)
}

func (app *application) cachedSandboxStats(sandboxID string) (sandboxStatsData, bool) {
	app.statsMutex.Lock()
	defer app.statsMutex.Unlock()
	entry, ok := app.statsCache[sandboxID]
	if !ok || time.Now().After(entry.expiresAt) {
		delete(app.statsCache, sandboxID)
		return sandboxStatsData{}, false
	}
	return entry.data, true
}

func (app *application) cacheSandboxStats(sandboxID string, data sandboxStatsData, duration time.Duration) {
	app.statsMutex.Lock()
	defer app.statsMutex.Unlock()
	app.statsCache[sandboxID] = cachedSandboxStats{data: data, expiresAt: time.Now().Add(duration)}
}

func (app *application) renderSandboxStats(w http.ResponseWriter, r *http.Request, data sandboxStatsData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := app.executeHTMLTemplate(w, app.statsTemplate, "", data); err != nil {
		slog.ErrorContext(r.Context(), "render sandbox stats", slog.String("sandbox_id", data.SandboxID), slog.Any("error", err))
	}
}

func parseSandboxStats(result opensandbox.CommandResult) (sandboxUsageStats, error) {
	if result.ExitCode != 0 {
		return sandboxUsageStats{}, fmt.Errorf("stats command exited with status %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))
	}

	values := make(map[string]string)
	for line := range strings.SplitSeq(result.Stdout, "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if found && key != "" {
			values[key] = value
		}
	}

	cpuStart, err := parseStatsUint(values, "cpu_start")
	if err != nil {
		return sandboxUsageStats{}, err
	}
	cpuEnd, err := parseStatsUint(values, "cpu_end")
	if err != nil {
		return sandboxUsageStats{}, err
	}
	if cpuEnd < cpuStart {
		return sandboxUsageStats{}, errors.New("sandbox CPU counter moved backwards")
	}
	cpuDeltaUS := float64(cpuEnd - cpuStart)
	if values["cpu_unit"] == "ns" {
		cpuDeltaUS /= 1_000
	} else if values["cpu_unit"] != "us" {
		return sandboxUsageStats{}, fmt.Errorf("unsupported sandbox CPU unit %q", values["cpu_unit"])
	}

	cpuCapacity := 0.0
	if values["cpu_quota"] != "" && values["cpu_quota"] != "max" && values["cpu_quota"] != "-1" {
		quota, parseErr := strconv.ParseFloat(values["cpu_quota"], 64)
		if parseErr != nil {
			return sandboxUsageStats{}, fmt.Errorf("parse sandbox CPU quota: %w", parseErr)
		}
		period, parseErr := strconv.ParseFloat(values["cpu_period"], 64)
		if parseErr != nil || period <= 0 {
			return sandboxUsageStats{}, fmt.Errorf("parse sandbox CPU period %q", values["cpu_period"])
		}
		cpuCapacity = quota / period
	}
	if cpuCapacity <= 0 {
		cpuCount, parseErr := strconv.ParseFloat(values["cpu_count"], 64)
		if parseErr != nil || cpuCount <= 0 {
			return sandboxUsageStats{}, fmt.Errorf("parse sandbox CPU count %q", values["cpu_count"])
		}
		cpuCapacity = cpuCount
	}
	cpuPercent := cpuDeltaUS / (float64(statsSampleUS) * cpuCapacity) * 100
	if cpuPercent < 0 {
		cpuPercent = 0
	}
	if cpuPercent > 100 {
		cpuPercent = 100
	}

	load1, err := strconv.ParseFloat(values["load_1"], 64)
	if err != nil || load1 < 0 {
		return sandboxUsageStats{}, fmt.Errorf("parse sandbox one-minute load %q", values["load_1"])
	}
	memoryCurrent, err := parseStatsUint(values, "memory_current")
	if err != nil {
		return sandboxUsageStats{}, err
	}
	usage := sandboxUsageStats{
		CPUPercent:    cpuPercent,
		CPUCapacity:   cpuCapacity,
		Load1:         load1,
		MemoryCurrent: memoryCurrent,
	}
	if values["memory_max"] != "max" {
		parsedMax, parseErr := parseStatsUint(values, "memory_max")
		if parseErr != nil {
			return sandboxUsageStats{}, parseErr
		}
		// Cgroup v1 represents an unlimited value with a number close to MaxInt64.
		if parsedMax < 1<<60 && parsedMax > 0 {
			usage.MemoryMax = parsedMax
			usage.MemoryLimited = true
		}
	}
	return usage, nil
}

func formatCPUCapacity(capacity float64) string {
	label := "cores"
	if capacity == 1 {
		label = "core"
	}
	return strconv.FormatFloat(capacity, 'f', -1, 64) + " " + label
}

func utilizationLevel(percent float64) string {
	if percent >= 90 {
		return "critical"
	}
	return "normal"
}

func parseStatsUint(values map[string]string, key string) (uint64, error) {
	value := values[key]
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse sandbox stat %s=%q: %w", key, value, err)
	}
	return parsed, nil
}

func formatByteCount(value uint64) string {
	const (
		kib = uint64(1 << 10)
		mib = uint64(1 << 20)
		gib = uint64(1 << 30)
	)
	switch {
	case value >= gib:
		return formatByteUnit(value, gib, "GiB")
	case value >= mib:
		return formatByteUnit(value, mib, "MiB")
	case value >= kib:
		return formatByteUnit(value, kib, "KiB")
	default:
		return fmt.Sprintf("%d B", value)
	}
}

func formatByteUnit(value, unit uint64, suffix string) string {
	amount := float64(value) / float64(unit)
	precision := 1
	if value%unit == 0 {
		precision = 0
	}
	return strconv.FormatFloat(amount, 'f', precision, 64) + " " + suffix
}

func (app *application) sandboxPTY(w http.ResponseWriter, r *http.Request) {
	sandboxID := r.PathValue("id")
	downstream, err := websocket.Accept(w, r, nil)
	if err != nil {
		slog.ErrorContext(r.Context(), "accept terminal WebSocket", slog.String("sandbox_id", sandboxID), slog.Any("error", err))
		return
	}
	defer downstream.Close(websocket.StatusNormalClosure, "")
	downstream.SetReadLimit(1 << 20)

	probeContext, cancelProbe := context.WithTimeout(r.Context(), 4*time.Second)
	probe, probeErr := app.sandboxCommands.RunCommand(probeContext, sandboxID, terminalShellProbeCommand)
	cancelProbe()
	if probeErr == nil && probe.ExitCode != 0 {
		message := "Interactive terminal requires Bash, but Bash is not installed in this sandbox."
		slog.WarnContext(r.Context(), "terminal shell unavailable", slog.String("sandbox_id", sandboxID))
		app.sendTerminalError(r.Context(), downstream, message)
		return
	}

	upstream, err := app.sandboxTerminal.OpenPTY(r.Context(), sandboxID)
	if err != nil {
		slog.ErrorContext(r.Context(), "open sandbox PTY", slog.String("sandbox_id", sandboxID), slog.Any("error", err))
		app.sendTerminalError(r.Context(), downstream, "Unable to open terminal: "+err.Error())
		return
	}
	defer upstream.Close(websocket.StatusNormalClosure, "")

	proxyContext, cancel := context.WithCancel(r.Context())
	defer cancel()
	proxyErrors := make(chan error, 2)
	go func() { proxyErrors <- copyWebSocket(proxyContext, upstream, downstream) }()
	go func() { proxyErrors <- copyWebSocket(proxyContext, downstream, upstream) }()
	if err := <-proxyErrors; err != nil && websocket.CloseStatus(err) == -1 {
		slog.ErrorContext(r.Context(), "proxy sandbox PTY", slog.String("sandbox_id", sandboxID), slog.Any("error", err))
	}
}

func (app *application) sendTerminalError(ctx context.Context, connection *websocket.Conn, message string) {
	payload, err := json.Marshal(map[string]string{
		"type":    "error",
		"message": message,
	})
	if err == nil {
		_ = connection.Write(ctx, websocket.MessageText, payload)
	}
	_ = connection.Close(websocket.StatusPolicyViolation, "Terminal unavailable")
}

func copyWebSocket(ctx context.Context, destination, source *websocket.Conn) error {
	for {
		messageType, message, err := source.Read(ctx)
		if err != nil {
			return err
		}
		if err := destination.Write(ctx, messageType, message); err != nil {
			return err
		}
	}
}

func sandboxDetailFromSandbox(sandbox opensandbox.Sandbox) sandboxDetailData {
	metadata := make([]detailItem, 0, len(sandbox.Metadata))
	for key, value := range sandbox.Metadata {
		if key == "createdBy" || strings.HasPrefix(key, "osb-dashboard/") {
			continue
		}
		metadata = append(metadata, detailItem{Label: key, Value: value})
	}
	sort.Slice(metadata, func(i, j int) bool { return metadata[i].Label < metadata[j].Label })

	return sandboxDetailData{
		ID:                sandbox.ID,
		State:             normalizeSandboxState(sandbox.State),
		StateLabel:        sandboxStateLabel(normalizeSandboxState(sandbox.State)),
		CreatedAtISO:      sandbox.CreatedAt.Format(time.RFC3339),
		CreatedAtFallback: sandbox.CreatedAt.Local().Format("2006-01-02 15:04:05 MST"),
		Namespace:         displayValue(sandbox.Namespace),
		PodName:           displayValue(sandbox.PodName),
		Image:             displayValue(sandbox.Image),
		Resources:         formatSandboxResources(sandbox.CPU, sandbox.Memory),
		Sources:           formatSandboxSources(sandbox.Sources),
		LifecycleManaged:  sandboxHasSource(sandbox, opensandbox.SourceLifecycle),
		Metadata:          metadata,
	}
}

func (app *application) sandboxDeploymentStatus(w http.ResponseWriter, r *http.Request) {
	sandboxID := r.PathValue("id")
	snapshotID := strings.TrimSpace(r.URL.Query().Get("snapshotId"))
	snapshotName := strings.TrimSpace(r.URL.Query().Get("snapshotName"))
	sandboxes, err := app.listSandboxes(r.Context(), true)
	var data sandboxDeploymentResultData
	for _, sandbox := range sandboxes {
		if sandbox.ID == sandboxID {
			data = sandboxDeploymentResultFromSandbox(sandbox, snapshotID, snapshotName, len(sandboxes))
			break
		}
	}
	if data.SandboxID == "" {
		message := "Waiting for the sandbox to appear."
		if err != nil {
			message = "Sandbox status is temporarily unavailable. Retrying…"
		}
		data = sandboxDeploymentResultData{
			SandboxID:    sandboxID,
			SnapshotID:   snapshotID,
			SnapshotName: displayValue(snapshotName),
			State:        "Checking",
			StateKey:     "checking",
			Message:      message,
			SandboxTotal: len(sandboxes),
			Polling:      true,
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := app.executeHTMLTemplate(w, app.deploymentResultTemplate, "deployment-result-status", data); err != nil {
		slog.ErrorContext(r.Context(), "render sandbox deployment status", slog.String("sandbox_id", sandboxID), slog.Any("error", err))
	}
}

func sandboxDeploymentResultFromSandbox(sandbox opensandbox.Sandbox, snapshotID, snapshotName string, total int) sandboxDeploymentResultData {
	stateKey := normalizeSandboxState(sandbox.State)
	ready := stateKey == "running"
	failed := stateKey == "failed" || stateKey == "canceled"
	message := "OpenSandbox is provisioning a sandbox from the snapshot."
	if ready {
		message = "The sandbox is running and ready to use."
	} else if failed {
		message = "OpenSandbox could not deploy the sandbox."
	}
	return sandboxDeploymentResultData{
		SandboxID:    sandbox.ID,
		SnapshotID:   snapshotID,
		SnapshotName: displayValue(snapshotName),
		State:        sandboxStateLabel(stateKey),
		StateKey:     stateKey,
		Message:      message,
		SandboxTotal: total,
		Polling:      !ready && !failed,
		Ready:        ready,
		Failed:       failed,
	}
}

func (app *application) renderSandboxDeploymentResult(w http.ResponseWriter, r *http.Request, data sandboxDeploymentResultData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("HX-Trigger", "sandboxDeploymentStarted")
	if err := app.executeHTMLTemplate(w, app.deploymentResultTemplate, "deployment-result", data); err != nil {
		slog.ErrorContext(r.Context(), "render sandbox deployment result", slog.String("sandbox_id", data.SandboxID), slog.Any("error", err))
	}
}

func (app *application) createSandbox(w http.ResponseWriter, r *http.Request) {
	request, err := app.createSandboxRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	requestID, err := createRequestID()
	if err != nil {
		http.Error(w, "Unable to create request identifier", http.StatusInternalServerError)
		return
	}
	request.Metadata["osb-dashboard/request-id"] = requestID
	deployingSnapshot := request.SnapshotID != ""
	snapshotName := strings.TrimSpace(r.FormValue("snapshotName"))
	if snapshotName == "" {
		snapshotName = request.SnapshotID
	}

	type createResult struct {
		sandbox opensandbox.Sandbox
		err     error
	}
	result := make(chan createResult, 1)
	app.background.Add(1)
	go func() {
		defer app.background.Done()
		createContext, cancel := context.WithTimeout(app.context, 6*time.Minute)
		defer cancel()
		sandbox, err := app.sandboxWriter.CreateSandbox(createContext, request)
		result <- createResult{sandbox: sandbox, err: err}
	}()

	poll := time.NewTicker(500 * time.Millisecond)
	defer poll.Stop()
	for {
		select {
		case createResult := <-result:
			if createResult.err != nil {
				slog.ErrorContext(r.Context(), "create sandbox", slog.Any("error", createResult.err))
				http.Error(w, "Unable to create sandbox: "+createResult.err.Error(), http.StatusBadGateway)
				return
			}
			if deployingSnapshot {
				app.invalidateSandboxCache()
				sandboxes, _ := app.listSandboxes(r.Context(), true)
				data := sandboxDeploymentResultFromSandbox(createResult.sandbox, request.SnapshotID, snapshotName, len(sandboxes))
				app.renderSandboxDeploymentResult(w, r, data)
				return
			}
			app.renderAcceptedSandbox(w, r.Context(), requestID, &createResult.sandbox)
			return
		case <-poll.C:
			sandboxes, listErr := app.listSandboxes(r.Context(), true)
			if sandbox := acceptedSandbox(sandboxes, requestID); sandbox != nil {
				if deployingSnapshot {
					data := sandboxDeploymentResultFromSandbox(*sandbox, request.SnapshotID, snapshotName, len(sandboxes))
					app.renderSandboxDeploymentResult(w, r, data)
					return
				}
				app.renderSandboxList(w, sandboxes, listErr)
				return
			}
		case <-r.Context().Done():
			return
		case <-app.context.Done():
			return
		}
	}
}

func (app *application) createSandboxRequest(r *http.Request) (opensandbox.CreateSandboxRequest, error) {
	if err := r.ParseForm(); err != nil {
		return opensandbox.CreateSandboxRequest{}, fmt.Errorf("parse create sandbox form: %w", err)
	}

	snapshotID := strings.TrimSpace(r.FormValue("snapshotId"))
	image := strings.TrimSpace(r.FormValue("image"))
	if snapshotID == "" && image == "" {
		image = app.sandboxImage
	}
	resourcePresets := map[string]map[string]string{
		"1core-2gib":  {"cpu": "1", "memory": "2Gi"},
		"2core-4gib":  {"cpu": "2", "memory": "4Gi"},
		"4core-8gib":  {"cpu": "4", "memory": "8Gi"},
		"8core-16gib": {"cpu": "8", "memory": "16Gi"},
	}
	resourcePreset := strings.TrimSpace(r.FormValue("resourcePreset"))
	if resourcePreset == "" {
		resourcePreset = "1core-2gib"
	}
	resourceLimits, ok := resourcePresets[resourcePreset]
	if !ok {
		return opensandbox.CreateSandboxRequest{}, errors.New("select a valid resource preset")
	}

	metadata := map[string]string{"createdBy": "osb-dashboard"}

	request := opensandbox.CreateSandboxRequest{
		Image:          image,
		SnapshotID:     snapshotID,
		ResourceLimits: resourceLimits,
		Metadata:       metadata,
	}
	if snapshotID == "" {
		request.Entrypoint = []string{"tail", "-f", "/dev/null"}
		return request, nil
	}
	if image != "" {
		return opensandbox.CreateSandboxRequest{}, errors.New("select either an image or a snapshot, not both")
	}
	return request, nil
}

func createRequestID() (string, error) {
	value := make([]byte, 12)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate create request ID: %w", err)
	}
	return hex.EncodeToString(value), nil
}

func acceptedSandbox(sandboxes []opensandbox.Sandbox, requestID string) *opensandbox.Sandbox {
	for index := range sandboxes {
		if sandboxes[index].Metadata["osb-dashboard/request-id"] != requestID {
			continue
		}
		state := strings.ToLower(sandboxes[index].State)
		if state == "pending" || state == "running" {
			return &sandboxes[index]
		}
	}
	return nil
}

func (app *application) renderAcceptedSandbox(
	w http.ResponseWriter,
	ctx context.Context,
	requestID string,
	created *opensandbox.Sandbox,
) {
	sandboxes, listErr := app.listSandboxes(ctx, true)
	if acceptedSandbox(sandboxes, requestID) == nil && created != nil {
		sandboxes = append(sandboxes, *created)
	}
	app.renderSandboxList(w, sandboxes, listErr)
}

func (app *application) renderSandboxList(w http.ResponseWriter, sandboxes []opensandbox.Sandbox, listErr error) {
	data := app.overviewDataFromSandboxes(sandboxes, "")
	if listErr != nil {
		data.Error = "Some sandbox sources could not be loaded: " + listErr.Error()
	}
	w.Header().Set("HX-Trigger", "sandboxCreateAccepted")
	app.renderOverview(w, data)
}

func (app *application) deleteSandbox(w http.ResponseWriter, r *http.Request) {
	sandboxID := r.PathValue("id")
	sandboxes, listErr := app.listSandboxes(r.Context(), true)
	var target *opensandbox.Sandbox
	for index := range sandboxes {
		if sandboxes[index].ID == sandboxID {
			target = &sandboxes[index]
			break
		}
	}
	if target == nil {
		data := app.overviewDataFromSandboxes(sandboxes, "")
		if listErr != nil {
			data.Error = "Unable to locate sandbox for deletion: " + listErr.Error()
		} else {
			data.Error = fmt.Sprintf("Sandbox %q was not found", sandboxID)
		}
		app.renderOverview(w, data)
		return
	}

	if err := app.sandboxWriter.DeleteSandbox(r.Context(), *target); err != nil {
		slog.ErrorContext(
			r.Context(),
			"delete sandbox",
			slog.String("sandbox_id", sandboxID),
			slog.Any("error", err),
		)
		data := app.overviewDataFromSandboxes(sandboxes, "")
		data.Error = "Unable to delete sandbox: " + err.Error()
		app.renderOverview(w, data)
		return
	}

	app.invalidateSandboxCache()
	app.statsMutex.Lock()
	delete(app.statsCache, sandboxID)
	app.statsMutex.Unlock()
	app.renderOverview(w, app.loadOverviewDataExcluding(r.Context(), sandboxID))
}

func (app *application) listSandboxes(ctx context.Context, fresh bool) ([]opensandbox.Sandbox, error) {
	app.sandboxCacheMutex.Lock()
	defer app.sandboxCacheMutex.Unlock()
	if !fresh && time.Now().Before(app.sandboxCacheUntil) {
		return append([]opensandbox.Sandbox(nil), app.sandboxCache...), app.sandboxCacheErr
	}
	sandboxes, err := app.sandboxReader.ListSandboxes(ctx)
	app.sandboxCache = append(app.sandboxCache[:0], sandboxes...)
	app.sandboxCacheErr = err
	cacheDuration := 4 * time.Second
	if err != nil {
		cacheDuration = time.Second
	}
	app.sandboxCacheUntil = time.Now().Add(cacheDuration)
	return append([]opensandbox.Sandbox(nil), sandboxes...), err
}

func (app *application) invalidateSandboxCache() {
	app.sandboxCacheMutex.Lock()
	app.sandboxCacheUntil = time.Time{}
	app.sandboxCacheMutex.Unlock()
}

func (app *application) listSnapshots(ctx context.Context, fresh bool) ([]opensandbox.Snapshot, error) {
	app.snapshotCacheMutex.Lock()
	defer app.snapshotCacheMutex.Unlock()
	if !fresh && time.Now().Before(app.snapshotCacheUntil) {
		return append([]opensandbox.Snapshot(nil), app.snapshotCache...), app.snapshotCacheErr
	}
	snapshots, err := app.sandboxReader.ListSnapshots(ctx)
	app.snapshotCache = append(app.snapshotCache[:0], snapshots...)
	app.snapshotCacheErr = err
	cacheDuration := 4 * time.Second
	if err != nil {
		cacheDuration = time.Second
	}
	app.snapshotCacheUntil = time.Now().Add(cacheDuration)
	return append([]opensandbox.Snapshot(nil), snapshots...), err
}

func (app *application) invalidateSnapshotCache() {
	app.snapshotCacheMutex.Lock()
	app.snapshotCacheUntil = time.Time{}
	app.snapshotCacheMutex.Unlock()
}

func (app *application) loadSnapshotsData(ctx context.Context, fresh bool) snapshotsData {
	snapshots, err := app.listSnapshots(ctx, fresh)
	data := app.snapshotsDataFromSnapshots(ctx, snapshots)
	if err != nil {
		slog.ErrorContext(ctx, "list snapshots", slog.Any("error", err))
		data.Error = "Snapshots could not be loaded: " + err.Error()
	}
	return data
}

func (app *application) snapshotsDataFromSnapshots(ctx context.Context, snapshots []opensandbox.Snapshot) snapshotsData {
	sandboxes, _ := app.listSandboxes(ctx, false)
	availableSandboxes := make(map[string]bool, len(sandboxes))
	for _, sandbox := range sandboxes {
		availableSandboxes[sandbox.ID] = true
	}
	views := make([]snapshotView, 0, len(snapshots))
	for _, snapshot := range snapshots {
		views = append(views, snapshotToView(snapshot, availableSandboxes[snapshot.SandboxID]))
	}
	return newSnapshotsData(views, len(sandboxes))
}

func snapshotToView(snapshot opensandbox.Snapshot, sourceSandboxAvailable bool) snapshotView {
	state := normalizeSandboxState(snapshot.State)
	name := snapshot.Name
	hasName := name != ""
	if !hasName {
		name = snapshot.ID
	}
	view := snapshotView{
		ID:                     snapshot.ID,
		SandboxID:              snapshot.SandboxID,
		Name:                   name,
		HasName:                hasName,
		State:                  state,
		StateLabel:             sandboxStateLabel(state),
		Reason:                 snapshot.Reason,
		Message:                snapshot.Message,
		CreatedAtISO:           snapshot.CreatedAt.Format(time.RFC3339),
		CreatedAtFallback:      snapshot.CreatedAt.Local().Format("2006-01-02 15:04:05 MST"),
		SourceSandboxAvailable: sourceSandboxAvailable,
		CanRestore:             state == "ready",
		CanDelete:              state != "creating" && state != "deleting",
	}
	if !snapshot.LastTransitionAt.IsZero() {
		view.LastTransitionAtISO = snapshot.LastTransitionAt.Format(time.RFC3339)
		view.LastTransitionAtFallback = snapshot.LastTransitionAt.Local().Format("2006-01-02 15:04:05 MST")
	}
	return view
}

func (app *application) renderSnapshots(w http.ResponseWriter, r *http.Request, data snapshotsData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := app.executeHTMLTemplate(w, app.snapshotsTemplate, "", data); err != nil {
		slog.ErrorContext(r.Context(), "render snapshots", slog.Any("error", err))
	}
}

func (app *application) loadOverviewData(ctx context.Context) overviewData {
	return app.loadOverviewDataExcluding(ctx, "")
}

func (app *application) loadOverviewDataExcluding(ctx context.Context, excludedID string) overviewData {
	sandboxes, err := app.listSandboxes(ctx, false)
	snapshots, _ := app.listSnapshots(ctx, false)
	data := app.overviewDataFromSandboxes(sandboxes, excludedID)
	data.SnapshotTotal = len(snapshots)
	if err != nil {
		slog.ErrorContext(ctx, "list sandboxes", slog.Any("error", err))
		data.Error = "Some sandbox sources could not be loaded: " + err.Error()
	}
	return data
}

func (app *application) overviewDataFromSandboxes(sandboxes []opensandbox.Sandbox, excludedID string) overviewData {
	views := make([]sandboxView, 0, len(sandboxes))
	for _, sandbox := range sandboxes {
		if sandbox.ID == excludedID {
			continue
		}
		views = append(views, sandboxToView(sandbox))
	}
	return newOverviewData(views)
}

func (app *application) renderOverview(w http.ResponseWriter, data overviewData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := app.executeHTMLTemplate(w, app.overviewTemplate, "", data); err != nil {
		slog.Error("render overview", slog.Any("error", err))
	}
}

func sandboxToView(sandbox opensandbox.Sandbox) sandboxView {
	name := sandbox.ID
	if metadataName := sandbox.Metadata["name"]; metadataName != "" {
		name = metadataName
	}
	return sandboxView{
		ID:                sandbox.ID,
		Name:              name,
		State:             sandbox.State,
		CreatedAtISO:      sandbox.CreatedAt.Format(time.RFC3339),
		CreatedAtFallback: sandbox.CreatedAt.Local().Format("2006-01-02 15:04:05 MST"),
		Namespace:         displayValue(sandbox.Namespace),
		PodName:           displayValue(sandbox.PodName),
		Image:             displayValue(sandbox.Image),
		Resources:         formatSandboxResources(sandbox.CPU, sandbox.Memory),
	}
}

func sandboxHasSource(sandbox opensandbox.Sandbox, wanted string) bool {
	for _, source := range sandbox.Sources {
		if source == wanted {
			return true
		}
	}
	return false
}

func formatSandboxSources(sources []string) string {
	labels := make([]string, 0, len(sources))
	for _, source := range sources {
		switch source {
		case opensandbox.SourceLifecycle:
			labels = append(labels, "Lifecycle API")
		case opensandbox.SourceBatchSandbox:
			labels = append(labels, "BatchSandbox")
		case opensandbox.SourceAgentSandbox:
			labels = append(labels, "Agent Sandbox")
		default:
			labels = append(labels, source)
		}
	}
	if len(labels) == 0 {
		return "—"
	}
	return strings.Join(labels, " + ")
}

func formatSandboxResources(cpu, memory string) string {
	var values []string
	if cpu != "" {
		if cores, err := strconv.Atoi(cpu); err == nil {
			label := "cores"
			if cores == 1 {
				label = "core"
			}
			values = append(values, fmt.Sprintf("%d %s", cores, label))
		} else {
			values = append(values, cpu+" CPU")
		}
	}
	if memory != "" {
		memory = strings.TrimSpace(memory)
		if strings.HasSuffix(memory, "Gi") {
			memory = strings.TrimSuffix(memory, "Gi") + " GiB"
		} else if strings.HasSuffix(memory, "Mi") {
			memory = strings.TrimSuffix(memory, "Mi") + " MiB"
		}
		values = append(values, memory)
	}
	if len(values) == 0 {
		return "—"
	}
	return strings.Join(values, " / ")
}

func displayValue(value string) string {
	if value == "" {
		return "—"
	}
	return value
}

func newOverviewData(sandboxes []sandboxView) overviewData {
	byState := make(map[string][]sandboxView)
	for _, sandbox := range sandboxes {
		state := normalizeSandboxState(sandbox.State)
		sandbox.State = state
		byState[state] = append(byState[state], sandbox)
	}

	states := append([]string(nil), defaultSandboxStates...)
	knownStates := make(map[string]bool, len(states))
	for _, state := range states {
		knownStates[state] = true
	}

	var additionalStates []string
	for state := range byState {
		if !knownStates[state] {
			additionalStates = append(additionalStates, state)
		}
	}
	sort.Strings(additionalStates)
	states = append(states, additionalStates...)

	data := overviewData{Total: len(sandboxes)}
	for _, state := range states {
		stateSandboxes := byState[state]
		if len(stateSandboxes) == 0 {
			continue
		}
		data.StateCounts = append(data.StateCounts, sandboxStateCount{
			State: state,
			Count: len(stateSandboxes),
		})
		data.Groups = append(data.Groups, sandboxGroup{
			State:     state,
			Label:     sandboxStateLabel(state),
			Sandboxes: stateSandboxes,
		})
	}

	return data
}

func newSnapshotsData(snapshots []snapshotView, sandboxTotal int) snapshotsData {
	byState := make(map[string][]snapshotView)
	for _, snapshot := range snapshots {
		byState[snapshot.State] = append(byState[snapshot.State], snapshot)
	}

	states := append([]string(nil), defaultSnapshotStates...)
	knownStates := make(map[string]bool, len(states))
	for _, state := range states {
		knownStates[state] = true
	}
	var additionalStates []string
	for state := range byState {
		if !knownStates[state] {
			additionalStates = append(additionalStates, state)
		}
	}
	sort.Strings(additionalStates)
	states = append(states, additionalStates...)

	data := snapshotsData{Total: len(snapshots), SandboxTotal: sandboxTotal}
	for _, state := range states {
		stateSnapshots := byState[state]
		if len(stateSnapshots) == 0 {
			continue
		}
		data.StateCounts = append(data.StateCounts, snapshotStateCount{State: state, Count: len(stateSnapshots)})
		data.Groups = append(data.Groups, snapshotGroup{
			State:     state,
			Label:     sandboxStateLabel(state),
			Snapshots: stateSnapshots,
		})
	}
	return data
}

func normalizeSandboxState(state string) string {
	state = strings.ToLower(strings.TrimSpace(state))
	if state == "" {
		return "unknown"
	}
	return state
}

func sandboxStateLabel(state string) string {
	words := strings.Fields(strings.ReplaceAll(state, "-", " "))
	for index, word := range words {
		words[index] = strings.ToUpper(word[:1]) + word[1:]
	}
	return strings.Join(words, " ")
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'wasm-unsafe-eval'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self' data: ws: wss:; object-src 'none'; base-uri 'self'; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func isLoopbackAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func tokenAuthentication(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		candidate := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if _, password, ok := r.BasicAuth(); ok {
			candidate = password
		}
		if subtle.ConstantTimeCompare([]byte(candidate), []byte(token)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="OpenSandbox Dashboard"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func csrfProtection(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		protected := r.Method == http.MethodPost || r.Method == http.MethodDelete ||
			strings.HasSuffix(r.URL.Path, "/terminal/pty")
		if !protected {
			next.ServeHTTP(w, r)
			return
		}
		if strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") {
			http.Error(w, "Cross-site request rejected", http.StatusForbidden)
			return
		}
		origin := r.Header.Get("Origin")
		if origin == "" {
			if r.Header.Get("X-OSB-CSRF") == "1" {
				next.ServeHTTP(w, r)
				return
			}
			http.Error(w, "Origin header is required", http.StatusForbidden)
			return
		}
		parsedOrigin, err := url.Parse(origin)
		if err != nil || !strings.EqualFold(parsedOrigin.Host, r.Host) {
			http.Error(w, "Cross-origin request rejected", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}
