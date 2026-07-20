package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
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
	assets            http.Handler
	kubeconfigPath    string
	indexTemplate     *template.Template
	overviewTemplate  *template.Template
	sandboxTemplate   *template.Template
	statsTemplate     *template.Template
	sandboxReader     opensandbox.Reader
	sandboxWriter     opensandbox.Writer
	sandboxTerminal   opensandbox.Terminal
	sandboxCommands   opensandbox.CommandRunner
	sandboxImage      string
	context           context.Context
	background        sync.WaitGroup
	statsMutex        sync.Mutex
	statsCache        map[string]cachedSandboxStats
	sandboxCacheMutex sync.Mutex
	sandboxCache      []opensandbox.Sandbox
	sandboxCacheErr   error
	sandboxCacheUntil time.Time
}

type commandConfig struct {
	kubeconfigPath       string
	openSandboxNamespace string
	sandboxNamespace     string
	sandboxImage         string
	authToken            string
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
	Total       int
	StateCounts []sandboxStateCount
	Groups      []sandboxGroup
	Error       string
}

type pageData struct {
	SandboxImage string
	SandboxID    string
	ContentURL   string
}

type detailItem struct {
	Label string
	Value string
}

type sandboxDetailData struct {
	ID                string
	Total             int
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
	app, err := newApplication(config.kubeconfigPath, client, client, client, client, config.sandboxImage, appContext)
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

func newApplication(
	kubeconfigPath string,
	sandboxReader opensandbox.Reader,
	sandboxWriter opensandbox.Writer,
	sandboxTerminal opensandbox.Terminal,
	sandboxCommands opensandbox.CommandRunner,
	sandboxImage string,
	appContext context.Context,
) (*application, error) {
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

	statsTemplate, err := template.ParseFS(webFiles, "web/stats.html")
	if err != nil {
		return nil, fmt.Errorf("parse stats template: %w", err)
	}

	return &application{
		assets:           http.FileServer(http.FS(assetsFS)),
		kubeconfigPath:   kubeconfigPath,
		indexTemplate:    indexTemplate,
		overviewTemplate: overviewTemplate,
		sandboxTemplate:  sandboxTemplate,
		statsTemplate:    statsTemplate,
		sandboxReader:    sandboxReader,
		sandboxWriter:    sandboxWriter,
		sandboxTerminal:  sandboxTerminal,
		sandboxCommands:  sandboxCommands,
		sandboxImage:     sandboxImage,
		context:          appContext,
		statsCache:       make(map[string]cachedSandboxStats),
	}, nil
}

func (app *application) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", app.index)
	mux.HandleFunc("GET /dashboard/overview", app.overview)
	mux.HandleFunc("GET /dashboard/sandboxes/{id}", app.sandboxPage)
	mux.HandleFunc("GET /dashboard/sandboxes/{id}/fragment", app.sandboxDetail)
	mux.HandleFunc("GET /dashboard/sandboxes/{id}/stats", app.sandboxStats)
	mux.HandleFunc("GET /dashboard/sandboxes/{id}/terminal/pty", app.sandboxPTY)
	mux.HandleFunc("POST /dashboard/sandboxes/{id}/pause", app.pauseSandbox)
	mux.HandleFunc("POST /dashboard/sandboxes/{id}/resume", app.resumeSandbox)
	mux.HandleFunc("POST /dashboard/sandboxes", app.createSandbox)
	mux.HandleFunc("DELETE /dashboard/sandboxes/{id}", app.deleteSandbox)
	mux.HandleFunc("GET /healthz", health)
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", app.assets))

	return mux
}

func (app *application) index(w http.ResponseWriter, r *http.Request) {
	app.renderPage(w, r, "")
}

func (app *application) sandboxPage(w http.ResponseWriter, r *http.Request) {
	app.renderPage(w, r, r.PathValue("id"))
}

func (app *application) renderPage(w http.ResponseWriter, r *http.Request, sandboxID string) {
	data := pageData{
		SandboxImage: app.sandboxImage,
		SandboxID:    sandboxID,
		ContentURL:   "/dashboard/overview",
	}
	if sandboxID != "" {
		data.ContentURL = "/dashboard/sandboxes/" + url.PathEscape(sandboxID) + "/fragment"
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := app.indexTemplate.Execute(w, data); err != nil {
		slog.ErrorContext(r.Context(), "render index", slog.Any("error", err))
	}
}

func (app *application) overview(w http.ResponseWriter, r *http.Request) {
	app.renderOverview(w, app.loadOverviewData(r.Context()))
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
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := app.sandboxTemplate.Execute(w, data); err != nil {
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
	if err := app.statsTemplate.Execute(w, data); err != nil {
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

	upstream, err := app.sandboxTerminal.OpenPTY(r.Context(), sandboxID)
	if err != nil {
		slog.ErrorContext(r.Context(), "open sandbox PTY", slog.String("sandbox_id", sandboxID), slog.Any("error", err))
		_ = downstream.Close(websocket.StatusInternalError, "Unable to open sandbox PTY")
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
			app.renderAcceptedSandbox(w, r.Context(), requestID, &createResult.sandbox)
			return
		case <-poll.C:
			sandboxes, listErr := app.listSandboxes(r.Context(), true)
			if sandbox := acceptedSandbox(sandboxes, requestID); sandbox != nil {
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

	image := strings.TrimSpace(r.FormValue("image"))
	if image == "" {
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

	return opensandbox.CreateSandboxRequest{
		Image:          image,
		Entrypoint:     []string{"tail", "-f", "/dev/null"},
		ResourceLimits: resourceLimits,
		Metadata:       metadata,
	}, nil
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

func (app *application) loadOverviewData(ctx context.Context) overviewData {
	return app.loadOverviewDataExcluding(ctx, "")
}

func (app *application) loadOverviewDataExcluding(ctx context.Context, excludedID string) overviewData {
	sandboxes, err := app.listSandboxes(ctx, false)
	data := app.overviewDataFromSandboxes(sandboxes, excludedID)
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
	if err := app.overviewTemplate.Execute(w, data); err != nil {
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
