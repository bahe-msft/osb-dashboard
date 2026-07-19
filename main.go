package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
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
	overviewTemplate *template.Template
	sandboxReader    opensandbox.Reader
	sandboxWriter    opensandbox.Writer
	sandboxImage     string
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

var defaultSandboxStates = []string{"pending", "running", "paused", "failed"}

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		log.Fatal(err)
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
	})
	if err != nil {
		return fmt.Errorf("create OpenSandbox client: %w", err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			log.Printf("close OpenSandbox client: %v", err)
		}
	}()

	app, err := newApplication(config.kubeconfigPath, client, client, config.sandboxImage)
	if err != nil {
		return err
	}

	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = defaultHTTPAddr
	}

	server := &http.Server{
		Addr:              addr,
		Handler:           app.routes(),
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
			log.Printf("graceful shutdown: %v", err)
		}
	}()

	log.Printf("OpenSandbox dashboard listening on %s using kubeconfig %s", addr, app.kubeconfigPath)
	if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve dashboard: %w", err)
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
) (*application, error) {
	assetsFS, err := fs.Sub(webFiles, "web/assets")
	if err != nil {
		return nil, fmt.Errorf("load web assets: %w", err)
	}

	overviewTemplate, err := template.ParseFS(webFiles, "web/overview.html")
	if err != nil {
		return nil, fmt.Errorf("parse overview template: %w", err)
	}

	return &application{
		assets:           http.FileServer(http.FS(assetsFS)),
		kubeconfigPath:   kubeconfigPath,
		overviewTemplate: overviewTemplate,
		sandboxReader:    sandboxReader,
		sandboxWriter:    sandboxWriter,
		sandboxImage:     sandboxImage,
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
	page, err := webFiles.ReadFile("web/index.html")
	if err != nil {
		http.Error(w, "unable to load dashboard", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(page)
}

func (app *application) overview(w http.ResponseWriter, r *http.Request) {
	app.renderOverview(w, app.loadOverviewData(r.Context()))
}

func (app *application) createSandbox(w http.ResponseWriter, r *http.Request) {
	created, err := app.sandboxWriter.CreateSandbox(r.Context(), opensandbox.CreateSandboxRequest{
		Image:      app.sandboxImage,
		Entrypoint: []string{"tail", "-f", "/dev/null"},
		Timeout:    10 * time.Minute,
		ResourceLimits: map[string]string{
			"cpu":    "250m",
			"memory": "256Mi",
		},
		Metadata: map[string]string{
			"createdBy": "osb-dashboard",
		},
	})
	if err != nil {
		log.Printf("create sandbox: %v", err)
		data := app.loadOverviewData(r.Context())
		data.Error = "Unable to deploy sandbox: " + err.Error()
		app.renderOverview(w, data)
		return
	}

	data := app.loadOverviewData(r.Context())
	if data.Error != "" {
		data = newOverviewData([]sandboxView{sandboxToView(created)})
	}
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
		log.Printf("delete sandbox %s: %v", sandboxID, err)
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
		log.Printf("list sandboxes: %v", err)
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
		log.Printf("render overview: %v", err)
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
	}
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
