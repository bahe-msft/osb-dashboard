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
)

const defaultHTTPAddr = ":8080"

//go:embed web
var webFiles embed.FS

type application struct {
	assets           http.Handler
	kubeconfigPath   string
	overviewTemplate *template.Template
}

type commandConfig struct {
	kubeconfigPath string
}

type sandboxView struct {
	Name     string
	State    string
	Detail   string
	Metadata string
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
}

var defaultSandboxStates = []string{"running", "paused", "failed"}

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

	app, err := newApplication(config.kubeconfigPath)
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

	if err := flags.Parse(args); err != nil {
		return commandConfig{}, err
	}
	if flags.NArg() != 0 {
		return commandConfig{}, fmt.Errorf("unexpected positional arguments: %v", flags.Args())
	}
	if config.kubeconfigPath == "" {
		return commandConfig{}, errors.New("--kubeconfig is required")
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

func newApplication(kubeconfigPath string) (*application, error) {
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
	}, nil
}

func (app *application) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", app.index)
	mux.HandleFunc("GET /dashboard/overview", app.overview)
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
	data := newOverviewData(nil)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := app.overviewTemplate.Execute(w, data); err != nil {
		log.Printf("render overview: %v", err)
	}
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
		data.StateCounts = append(data.StateCounts, sandboxStateCount{
			State: state,
			Count: len(stateSandboxes),
		})
		if len(stateSandboxes) == 0 {
			continue
		}
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
