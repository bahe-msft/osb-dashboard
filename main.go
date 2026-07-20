package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
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
)

const defaultHTTPAddr = ":8080"

//go:embed web
var webFiles embed.FS

type application struct {
	assets           http.Handler
	kubeconfigPath   string
	indexTemplate    *template.Template
	overviewTemplate *template.Template
	sandboxReader    opensandbox.Reader
	sandboxWriter    opensandbox.Writer
	sandboxImage     string
	context          context.Context
	background       sync.WaitGroup
}

type commandConfig struct {
	kubeconfigPath       string
	openSandboxNamespace string
	sandboxNamespace     string
	sandboxImage         string
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
	app, err := newApplication(config.kubeconfigPath, client, client, config.sandboxImage, appContext)
	if err != nil {
		cancelApp()
		return err
	}

	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = defaultHTTPAddr
	}

	server := &http.Server{
		Addr:              addr,
		Handler:           requestLogger(slog.Default(), app.routes()),
		ReadHeaderTimeout: 5 * time.Second,
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

	return &application{
		assets:           http.FileServer(http.FS(assetsFS)),
		kubeconfigPath:   kubeconfigPath,
		indexTemplate:    indexTemplate,
		overviewTemplate: overviewTemplate,
		sandboxReader:    sandboxReader,
		sandboxWriter:    sandboxWriter,
		sandboxImage:     sandboxImage,
		context:          appContext,
	}, nil
}

func (app *application) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", app.index)
	mux.HandleFunc("GET /dashboard/overview", app.overview)
	mux.HandleFunc("POST /dashboard/sandboxes", app.createSandbox)
	mux.HandleFunc("DELETE /dashboard/sandboxes/{id}", app.deleteSandbox)
	mux.HandleFunc("GET /healthz", health)
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", app.assets))

	return mux
}

func (app *application) index(w http.ResponseWriter, r *http.Request) {
	data := struct {
		SandboxImage string
	}{
		SandboxImage: app.sandboxImage,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := app.indexTemplate.Execute(w, data); err != nil {
		slog.ErrorContext(r.Context(), "render index", slog.Any("error", err))
	}
}

func (app *application) overview(w http.ResponseWriter, r *http.Request) {
	app.renderOverview(w, app.loadOverviewData(r.Context()))
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
			sandboxes, listErr := app.sandboxReader.ListSandboxes(r.Context())
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
	sandboxes, listErr := app.sandboxReader.ListSandboxes(ctx)
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
	sandboxes, listErr := app.sandboxReader.ListSandboxes(r.Context())
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

	app.renderOverview(w, app.loadOverviewDataExcluding(r.Context(), sandboxID))
}

func (app *application) loadOverviewData(ctx context.Context) overviewData {
	return app.loadOverviewDataExcluding(ctx, "")
}

func (app *application) loadOverviewDataExcluding(ctx context.Context, excludedID string) overviewData {
	sandboxes, err := app.sandboxReader.ListSandboxes(ctx)
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

func health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}
