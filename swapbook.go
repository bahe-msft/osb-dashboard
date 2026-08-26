//go:build swapbook

package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"text/template"
	"time"

	adapter "github.com/Aejkatappaja/swapbook/adapters/go"
)

// NewSwapbookHandler returns the development-only target consumed by the
// Swapbook proxy. Build it with the "swapbook" tag; it is not part of normal
// dashboard binaries.
func NewSwapbookHandler() (http.Handler, error) {
	index, err := template.ParseFS(webFiles, "web/index.html")
	if err != nil {
		return nil, fmt.Errorf("parse Swapbook index template: %w", err)
	}
	overview, err := template.ParseFS(webFiles, "web/overview.html", "web/sandbox-row.html")
	if err != nil {
		return nil, fmt.Errorf("parse Swapbook overview template: %w", err)
	}
	pools, err := template.ParseFS(webFiles, "web/pools.html")
	if err != nil {
		return nil, fmt.Errorf("parse Swapbook pools template: %w", err)
	}
	pool, err := template.ParseFS(webFiles, "web/pool.html", "web/sandbox-row.html")
	if err != nil {
		return nil, fmt.Errorf("parse Swapbook pool template: %w", err)
	}
	snapshots, err := template.ParseFS(webFiles, "web/snapshots.html")
	if err != nil {
		return nil, fmt.Errorf("parse Swapbook snapshots template: %w", err)
	}
	stats, err := template.ParseFS(webFiles, "web/cluster-stats.html")
	if err != nil {
		return nil, fmt.Errorf("parse Swapbook stats template: %w", err)
	}
	sandbox, err := template.ParseFS(webFiles, "web/sandbox.html")
	if err != nil {
		return nil, fmt.Errorf("parse Swapbook sandbox template: %w", err)
	}

	now := time.Now()
	running := swapbookSandbox("sandbox-running", "running", "python:3.12-slim", "1 core / 2 GiB", "sandbox-running-0", now.Add(-4*time.Minute))
	pooled := swapbookSandbox("sandbox-pool-1", "running", "python:3.12-slim", "1 core / 2 GiB", "default-pool-a1b2c", now.Add(-2*time.Minute))

	capacityPool := swapbookPool("at-capacity", "At capacity", 0, 2, 2, now.Add(-time.Hour))
	capacityPoolDetail := poolDetailData{poolView: capacityPool, PoolTotal: 1, SandboxTotal: 2, ActiveSandboxes: []sandboxView{pooled, running}}

	emptySnapshots := newSnapshotsData(nil, 1)
	emptySnapshots.PoolTotal = 1
	readySnapshot := snapshotView{
		ID: "snapshot-ready", SandboxID: running.ID, SandboxIDShort: "sandbox-running",
		Name: "before-upgrade", HasName: true, State: "ready", StateLabel: "Ready",
		CreatedAtISO: now.Add(-30 * time.Minute).Format(time.RFC3339), CreatedAtFallback: "30 minutes ago",
		SourceSandboxAvailable: true, CanRestore: true, CanDelete: true,
	}
	readySnapshots := newSnapshotsData([]snapshotView{readySnapshot}, 1)
	readySnapshots.PoolTotal = 1

	clusterStats := clusterStatsData{
		SandboxTotal: 3, PoolTotal: 1, ScheduledTotal: 3, NodeTotal: 2, SandboxesPerNode: "1.5",
		Nodes: []clusterStatsNodeView{
			{Name: "kata-node-01", SandboxCount: 2, CPUReserved: "2 cores / 8 cores", CPUPercent: 25, MemoryReserved: "4 GiB / 32 GiB", MemoryPercent: 12.5},
			{Name: "kata-node-02", SandboxCount: 1, CPUReserved: "1 core / 8 cores", CPUPercent: 12.5, MemoryReserved: "2 GiB / 32 GiB", MemoryPercent: 6.25},
		},
	}

	sandboxDetail := sandboxDetailData{
		ID: running.ID, Total: 1, PoolTotal: 1, State: "running", StateLabel: "Running",
		CreatedAtISO: running.CreatedAtISO, CreatedAtFallback: running.CreatedAtFallback,
		Namespace: running.Namespace, PodName: running.PodName, Image: running.Image,
		Resources: running.Resources, Sources: "Lifecycle API + BatchSandbox", LifecycleManaged: true,
	}
	pooledDetail := sandboxDetail
	pooledDetail.ID = pooled.ID
	pooledDetail.ParentPool = "default-pool"
	pooledDetail.PoolRef = "default-pool"
	pooledDetail.PodName = pooled.PodName

	reg := adapter.New()
	reg.HTMXSrc = "/assets/third-party/ui/htmx.min.js"
	reg.CSSSrc = "/assets/styles.css"
	reg.JSSrc = "/assets/app.js"
	reg.Viewports = []adapter.Viewport{{Name: "dashboard", Width: "1440px"}, {Name: "compact", Width: "480px"}}

	reg.Mock("GET /dashboard/sandboxes/sandbox-running/fragment", swapbookRender(sandbox, sandboxDetail))
	for _, fixture := range []sandboxView{
		swapbookSandbox("sandbox-pending", "pending", "node:22-slim", "2 cores / 4 GiB", "sandbox-pending-0", now.Add(-45*time.Second)),
		swapbookSandbox("sandbox-paused", "paused", "python:3.12-slim", "1 core / 2 GiB", "sandbox-paused-0", now.Add(-8*time.Minute)),
		swapbookSandbox("sandbox-failed", "failed", "ubuntu:24.04", "1 core / 2 GiB", "sandbox-failed-0", now.Add(-12*time.Minute)),
	} {
		detail := swapbookSandboxDetail(fixture)
		reg.Mock("GET /dashboard/sandboxes/"+fixture.ID+"/fragment", swapbookRender(sandbox, detail))
	}
	reg.Mock("GET /dashboard/sandboxes/sandbox-running/fragment?pool=default-pool", swapbookRender(sandbox, sandboxDetail))
	reg.Mock("GET /dashboard/sandboxes/sandbox-pool-1/fragment?pool=default-pool", swapbookRender(sandbox, pooledDetail))
	reg.Mock("GET /dashboard/pools/default-pool/fragment", swapbookRender(pool, capacityPoolDetail))
	for _, view := range swapbookPoolsData(url.Values{
		"ready":      {"true"},
		"scaling":    {"true"},
		"atCapacity": {"true"},
		"degraded":   {"true"},
	}, now).Pools {
		detail := poolDetailData{poolView: view, PoolTotal: 4, SandboxTotal: 2}
		reg.Mock("GET /dashboard/pools/"+view.Name+"/fragment", swapbookRender(pool, detail))
	}

	sandboxStateControls := []adapter.Control{
		{Name: "running", Type: "bool", Default: true},
		{Name: "pending", Type: "bool", Default: true},
		{Name: "paused", Type: "bool", Default: false},
		{Name: "failed", Type: "bool", Default: false},
		{Name: "error", Type: "bool", Default: false},
	}
	reg.RegisterIn("foundations", "Iconography",
		adapter.Var("catalog", swapbookIconography()),
	)
	reg.DocStory("Iconography", "The complete Lucide icon inventory used by production dashboard templates and JavaScript.")

	reg.RegisterIn("pages", "Sandbox overview",
		adapter.VarC("states", sandboxStateControls, func(args adapter.Args) adapter.Renderer {
			return swapbookRender(index, pageData{
				SandboxImage: "python:3.12-slim", Page: "list",
				ContentURL: swapbookControlURL("/dashboard/overview", args, "running", "pending", "paused", "failed", "error"),
			})
		}),
	)
	reg.DocStory("Sandbox overview", "Toggle sandbox states independently. Turn every state off to preview the empty page; `error` adds the partial-source warning.")

	poolStateControls := []adapter.Control{
		{Name: "ready", Type: "bool", Default: true},
		{Name: "scaling", Type: "bool", Default: false},
		{Name: "atCapacity", Type: "bool", Default: false},
		{Name: "degraded", Type: "bool", Default: false},
	}
	reg.RegisterIn("pages", "Pools",
		adapter.VarC("states", poolStateControls, func(args adapter.Args) adapter.Renderer {
			return swapbookRender(index, pageData{
				SandboxImage: "python:3.12-slim", Page: "pools",
				ContentURL: swapbookControlURL("/dashboard/pools", args, "ready", "scaling", "atCapacity", "degraded"),
			})
		}),
	)
	reg.DocStory("Pools", "Toggle Pool states independently. Turn every state off to preview the empty page.")

	poolDetailControls := []adapter.Control{
		{Name: "state", Type: "select", Default: "ready", Options: []string{"ready", "scaling", "atCapacity", "degraded"}},
		{Name: "activeSandboxes", Type: "bool", Default: false},
	}
	reg.RegisterIn("pages", "Pool details",
		adapter.VarC("states", poolDetailControls, func(args adapter.Args) adapter.Renderer {
			return swapbookRender(index, pageData{
				SandboxImage: "python:3.12-slim", PoolName: "default-pool", Page: "pool-detail",
				ContentURL: swapbookDetailControlURL("/dashboard/swapbook/pool-detail", args, "activeSandboxes"),
			})
		}),
	)
	reg.DocStory("Pool details", "Select the Pool state and toggle allocated sandboxes independently.")

	reg.RegisterIn("pages", "Snapshots",
		swapbookPageVariant("empty", index, pageData{SandboxImage: "python:3.12-slim", Page: "snapshots", ContentURL: "/dashboard/snapshots"}, "GET /dashboard/snapshots", swapbookRender(snapshots, emptySnapshots)),
		swapbookPageVariant("ready", index, pageData{SandboxImage: "python:3.12-slim", Page: "snapshots", ContentURL: "/dashboard/snapshots"}, "GET /dashboard/snapshots", swapbookRender(snapshots, readySnapshots)),
	)

	reg.RegisterIn("pages", "Cluster stats",
		swapbookPageVariant("with nodes", index, pageData{SandboxImage: "python:3.12-slim", Page: "stats", ContentURL: "/dashboard/stats"}, "GET /dashboard/stats", swapbookRender(stats, clusterStats)),
	)

	sandboxDetailControls := []adapter.Control{
		{Name: "state", Type: "select", Default: "running", Options: []string{"running", "pending", "paused", "failed"}},
		{Name: "fromPool", Type: "bool", Default: false},
	}
	reg.RegisterIn("details", "Sandbox details",
		adapter.VarC("states", sandboxDetailControls, func(args adapter.Args) adapter.Renderer {
			fromPool := args.Bool("fromPool")
			sandboxID := "sandbox-running"
			parentPool := ""
			if fromPool {
				sandboxID = "sandbox-pool-1"
				parentPool = "default-pool"
			}
			return swapbookRender(index, pageData{
				SandboxImage: "python:3.12-slim", SandboxID: sandboxID, ParentPool: parentPool, Page: "detail",
				ContentURL: swapbookDetailControlURL("/dashboard/swapbook/sandbox-detail", args, "fromPool"),
			})
		}),
	)
	reg.DocStory("Sandbox details", "Select lifecycle state and toggle Pool ownership without maintaining separate variants.")

	assetsFS, err := fs.Sub(webFiles, "web/assets")
	if err != nil {
		return nil, fmt.Errorf("load Swapbook assets: %w", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /_swapbook/inspection.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(swapbookInspectionSpec())
	})
	mux.Handle(adapter.MountPath+"/", http.StripPrefix(adapter.MountPath, reg.Handler()))
	mux.HandleFunc("GET /dashboard/overview", func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := overview.Execute(w, swapbookOverviewData(request.URL.Query(), now)); err != nil {
			http.Error(w, "render sandbox overview: "+err.Error(), http.StatusInternalServerError)
		}
	})
	mux.HandleFunc("GET /dashboard/pools", func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := pools.Execute(w, swapbookPoolsData(request.URL.Query(), now)); err != nil {
			http.Error(w, "render pools: "+err.Error(), http.StatusInternalServerError)
		}
	})
	mux.HandleFunc("GET /dashboard/swapbook/pool-detail", func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := pool.Execute(w, swapbookPoolDetailData(request.URL.Query(), now)); err != nil {
			http.Error(w, "render pool detail: "+err.Error(), http.StatusInternalServerError)
		}
	})
	mux.HandleFunc("GET /dashboard/swapbook/sandbox-detail", func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := sandbox.Execute(w, swapbookSandboxDetailData(request.URL.Query(), now)); err != nil {
			http.Error(w, "render sandbox detail: "+err.Error(), http.StatusInternalServerError)
		}
	})
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(assetsFS))))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok\n") })
	return mux, nil
}

func swapbookIconography() adapter.Renderer {
	var content strings.Builder
	content.WriteString(`<!doctype html><html lang="en" data-theme="corporate"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>OpenSandbox iconography</title><link rel="icon" href="data:,"><link rel="stylesheet" href="/assets/third-party/ui/basecoat.min.css"><link rel="stylesheet" href="/assets/styles.css"><script src="/assets/third-party/ui/lucide.min.js" defer></script><style>html,body{height:auto;min-height:100%;overflow:auto}.iconography{max-width:80rem;margin:auto;padding:32px}.iconography h1{margin:0 0 8px;font-size:24px}.iconography>p{margin:0 0 28px;color:var(--os-muted)}.icon-group{margin:28px 0}.icon-group h2{margin:0 0 12px;font-size:15px;text-transform:capitalize}.icon-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(210px,1fr));gap:10px}.icon-card{display:grid;grid-template-columns:32px minmax(0,1fr);gap:10px;align-items:center;min-height:64px;padding:10px;border:1px solid var(--os-border);border-radius:8px;background:var(--os-panel)}.icon-card svg{width:20px;height:20px}.icon-card strong,.icon-card code,.icon-card small{display:block;overflow:hidden;text-overflow:ellipsis}.icon-card strong{font-size:12px}.icon-card code{margin-top:2px;color:var(--os-text);font:600 11px/1.4 ui-monospace,SFMono-Regular,Menlo,monospace}.icon-card small{margin-top:2px;color:var(--os-muted);font-size:10px}</style></head><body><main class="iconography"><h1>Iconography</h1><p>Lucide icons intentionally used by the OpenSandbox dashboard.</p>`)
	category := ""
	for _, icon := range dashboardIconCatalog {
		if icon.Category != category {
			if category != "" {
				content.WriteString(`</div></section>`)
			}
			category = icon.Category
			content.WriteString(`<section class="icon-group"><h2>` + template.HTMLEscapeString(category) + `</h2><div class="icon-grid">`)
		}
		content.WriteString(`<article class="icon-card"><i data-lucide="` + template.HTMLEscapeString(icon.Icon) + `" aria-hidden="true"></i><div><strong>` + template.HTMLEscapeString(icon.Target) + `</strong><code>` + template.HTMLEscapeString(icon.Icon) + `</code><small>` + template.HTMLEscapeString(icon.Purpose) + `</small></div></article>`)
	}
	if category != "" {
		content.WriteString(`</div></section>`)
	}
	content.WriteString(`</main><script>addEventListener('DOMContentLoaded',function(){lucide.createIcons()})</script></body></html>`)
	return adapter.HTML(content.String())
}

type swapbookInspectionDocument struct {
	Version   int                          `json:"version"`
	Viewports []swapbookInspectionViewport `json:"viewports"`
	Cases     []swapbookInspectionCase     `json:"cases"`
}

type swapbookInspectionViewport struct {
	Name   string `json:"name"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type swapbookInspectionCase struct {
	ID         string                        `json:"id"`
	Story      string                        `json:"story"`
	Variant    string                        `json:"variant"`
	Args       map[string]string             `json:"args,omitempty"`
	Viewports  []string                      `json:"viewports"`
	Sources    []string                      `json:"sources"`
	Assertions []swapbookInspectionAssertion `json:"assertions"`
}

type swapbookInspectionAssertion struct {
	Type      string `json:"type"`
	Selector  string `json:"selector"`
	Expected  any    `json:"expected,omitempty"`
	Attribute string `json:"attribute,omitempty"`
}

func swapbookInspectionSpec() swapbookInspectionDocument {
	allViewports := []string{"dashboard", "compact"}
	pageSources := []string{"web/index.html", "web/assets/styles.css", "web/assets/app.js"}
	return swapbookInspectionDocument{
		Version: 1,
		Viewports: []swapbookInspectionViewport{
			{Name: "dashboard", Width: 1440, Height: 1000},
			{Name: "compact", Width: 480, Height: 800},
		},
		Cases: []swapbookInspectionCase{
			{
				ID: "iconography", Story: "iconography", Variant: "catalog",
				Viewports: allViewports, Sources: []string{"icons.go", "docs/icons.md", "web/assets/styles.css"},
				Assertions: []swapbookInspectionAssertion{{Type: "count", Selector: ".icon-card", Expected: len(dashboardIconCatalog)}, {Type: "count", Selector: ".icon-card svg", Expected: len(dashboardIconCatalog)}, {Type: "text", Selector: ".iconography", Expected: "terminal-square"}},
			},
			{
				ID: "sandbox-list-empty", Story: "sandbox-overview", Variant: "states",
				Args:      map[string]string{"running": "false", "pending": "false", "paused": "false", "failed": "false", "error": "false"},
				Viewports: allViewports, Sources: append(append([]string{}, pageSources...), "web/overview.html", "web/sandbox-row.html"),
				Assertions: []swapbookInspectionAssertion{{Type: "visible", Selector: ".empty-state"}, {Type: "count", Selector: ".sandbox-row", Expected: 0}},
			},
			{
				ID: "sandbox-list-mixed", Story: "sandbox-overview", Variant: "states",
				Args:      map[string]string{"running": "true", "pending": "false", "paused": "true", "failed": "true", "error": "false"},
				Viewports: allViewports, Sources: append(append([]string{}, pageSources...), "web/overview.html", "web/sandbox-row.html"),
				Assertions: []swapbookInspectionAssertion{{Type: "count", Selector: ".sandbox-row", Expected: 3}, {Type: "visible", Selector: "[data-sandbox-state=paused]"}, {Type: "visible", Selector: "[data-sandbox-state=failed]"}},
			},
			{
				ID: "sandbox-list-error", Story: "sandbox-overview", Variant: "states",
				Args:      map[string]string{"running": "true", "pending": "false", "paused": "false", "failed": "false", "error": "true"},
				Viewports: allViewports, Sources: append(append([]string{}, pageSources...), "web/overview.html"),
				Assertions: []swapbookInspectionAssertion{{Type: "visible", Selector: ".dashboard-error"}, {Type: "text", Selector: ".dashboard-error", Expected: "lifecycle service unavailable"}},
			},
			{
				ID: "pool-list-empty", Story: "pools", Variant: "states",
				Args:      map[string]string{"ready": "false", "scaling": "false", "atCapacity": "false", "degraded": "false"},
				Viewports: allViewports, Sources: append(append([]string{}, pageSources...), "web/pools.html"),
				Assertions: []swapbookInspectionAssertion{{Type: "visible", Selector: ".pool-empty-state"}, {Type: "count", Selector: ".pool-row", Expected: 0}},
			},
			{
				ID: "pool-list-states", Story: "pools", Variant: "states",
				Args:      map[string]string{"ready": "true", "scaling": "true", "atCapacity": "true", "degraded": "true"},
				Viewports: allViewports, Sources: append(append([]string{}, pageSources...), "web/pools.html"),
				Assertions: []swapbookInspectionAssertion{{Type: "count", Selector: ".pool-row", Expected: 4}, {Type: "text", Selector: "#dashboard-content", Expected: "degraded-pool"}},
			},
			{
				ID: "pool-detail-ready", Story: "pool-details", Variant: "states",
				Args:      map[string]string{"state": "ready", "activeSandboxes": "false"},
				Viewports: allViewports, Sources: append(append([]string{}, pageSources...), "web/pool.html", "web/sandbox-row.html"),
				Assertions: []swapbookInspectionAssertion{{Type: "text", Selector: "#dashboard-content", Expected: "Ready"}, {Type: "count", Selector: ".pool-sandbox-row", Expected: 0}},
			},
			{
				ID: "pool-detail-capacity", Story: "pool-details", Variant: "states",
				Args:      map[string]string{"state": "atCapacity", "activeSandboxes": "true"},
				Viewports: allViewports, Sources: append(append([]string{}, pageSources...), "web/pool.html", "web/sandbox-row.html"),
				Assertions: []swapbookInspectionAssertion{{Type: "text", Selector: "#dashboard-content", Expected: "At capacity"}, {Type: "count", Selector: ".pool-sandbox-row", Expected: 2}, {Type: "attribute", Selector: "[data-deploy-pool]", Attribute: "disabled", Expected: ""}},
			},
			{
				ID: "sandbox-detail-running", Story: "sandbox-details", Variant: "states",
				Args:      map[string]string{"state": "running", "fromPool": "false"},
				Viewports: allViewports, Sources: append(append([]string{}, pageSources...), "web/sandbox.html"),
				Assertions: []swapbookInspectionAssertion{{Type: "text", Selector: ".sandbox-detail-header", Expected: "Running"}, {Type: "visible", Selector: "[data-terminal-connect]"}},
			},
			{
				ID: "sandbox-detail-pooled-paused", Story: "sandbox-details", Variant: "states",
				Args:      map[string]string{"state": "paused", "fromPool": "true"},
				Viewports: allViewports, Sources: append(append([]string{}, pageSources...), "web/sandbox.html"),
				Assertions: []swapbookInspectionAssertion{{Type: "text", Selector: ".sandbox-detail-header", Expected: "Paused"}, {Type: "text", Selector: "#navigation-title", Expected: "default-pool"}, {Type: "text", Selector: "[data-terminal-overlay]", Expected: "must be running"}},
			},
			{
				ID: "snapshots-empty", Story: "snapshots", Variant: "empty",
				Viewports: allViewports, Sources: append(append([]string{}, pageSources...), "web/snapshots.html"),
				Assertions: []swapbookInspectionAssertion{{Type: "visible", Selector: ".snapshot-empty-state"}, {Type: "count", Selector: ".snapshot-row", Expected: 0}},
			},
			{
				ID: "snapshots-ready", Story: "snapshots", Variant: "ready",
				Viewports: allViewports, Sources: append(append([]string{}, pageSources...), "web/snapshots.html"),
				Assertions: []swapbookInspectionAssertion{{Type: "count", Selector: ".snapshot-row", Expected: 1}, {Type: "text", Selector: ".snapshot-row", Expected: "before-upgrade"}},
			},
			{
				ID: "cluster-stats", Story: "cluster-stats", Variant: "with nodes",
				Viewports: allViewports, Sources: append(append([]string{}, pageSources...), "web/cluster-stats.html"),
				Assertions: []swapbookInspectionAssertion{{Type: "count", Selector: ".cluster-stat-card", Expected: 3}, {Type: "text", Selector: "#dashboard-content", Expected: "kata-node-01"}},
			},
		},
	}
}

type swapbookTemplateRenderer struct {
	template *template.Template
	data     any
}

func (r swapbookTemplateRenderer) Render(_ context.Context, w io.Writer) error {
	return r.template.Execute(w, r.data)
}

func swapbookRender(tmpl *template.Template, data any) adapter.Renderer {
	return swapbookTemplateRenderer{template: tmpl, data: data}
}

func swapbookPageVariant(name string, index *template.Template, page pageData, route string, fragment adapter.Renderer) adapter.Variant {
	return adapter.Var(name, swapbookRender(index, page)).Mock(route, fragment)
}

func swapbookControlURL(path string, args adapter.Args, names ...string) string {
	values := url.Values{}
	for _, name := range names {
		values.Set(name, strconv.FormatBool(args.Bool(name)))
	}
	return path + "?" + values.Encode()
}

func swapbookDetailControlURL(path string, args adapter.Args, boolNames ...string) string {
	values := url.Values{"state": {args.String("state")}}
	for _, name := range boolNames {
		values.Set(name, strconv.FormatBool(args.Bool(name)))
	}
	return path + "?" + values.Encode()
}

func swapbookOverviewData(values url.Values, now time.Time) overviewData {
	defaults := map[string]bool{"running": true, "pending": true}
	var sandboxes []sandboxView
	if swapbookQueryBool(values, "running", defaults) {
		sandboxes = append(sandboxes, swapbookSandbox("sandbox-running", "running", "python:3.12-slim", "1 core / 2 GiB", "sandbox-running-0", now.Add(-4*time.Minute)))
	}
	if swapbookQueryBool(values, "pending", defaults) {
		sandboxes = append(sandboxes, swapbookSandbox("sandbox-pending", "pending", "node:22-slim", "2 cores / 4 GiB", "sandbox-pending-0", now.Add(-45*time.Second)))
	}
	if swapbookQueryBool(values, "paused", defaults) {
		sandboxes = append(sandboxes, swapbookSandbox("sandbox-paused", "paused", "python:3.12-slim", "1 core / 2 GiB", "sandbox-paused-0", now.Add(-8*time.Minute)))
	}
	if swapbookQueryBool(values, "failed", defaults) {
		sandboxes = append(sandboxes, swapbookSandbox("sandbox-failed", "failed", "ubuntu:24.04", "1 core / 2 GiB", "sandbox-failed-0", now.Add(-12*time.Minute)))
	}
	data := newOverviewData(sandboxes)
	data.PoolTotal = 1
	if swapbookQueryBool(values, "error", defaults) {
		data.Error = "Some sandbox sources could not be loaded: lifecycle service unavailable"
	}
	return data
}

func swapbookPoolsData(values url.Values, now time.Time) poolsData {
	defaults := map[string]bool{"ready": true}
	var views []poolView
	if swapbookQueryBool(values, "ready", defaults) {
		view := swapbookPool("ready", "Ready", 1, 0, 1, now.Add(-time.Hour))
		view.Name = "ready-pool"
		views = append(views, view)
	}
	if swapbookQueryBool(values, "scaling", defaults) {
		view := swapbookPool("pending", "Scaling", 0, 1, 1, now.Add(-40*time.Minute))
		view.Name = "scaling-pool"
		views = append(views, view)
	}
	if swapbookQueryBool(values, "atCapacity", defaults) {
		view := swapbookPool("at-capacity", "At capacity", 0, 2, 2, now.Add(-2*time.Hour))
		view.Name = "capacity-pool"
		views = append(views, view)
	}
	if swapbookQueryBool(values, "degraded", defaults) {
		view := swapbookPool("degraded", "Degraded", 0, 1, 2, now.Add(-3*time.Hour))
		view.Name = "degraded-pool"
		views = append(views, view)
	}
	return poolsData{Total: len(views), SandboxTotal: 2, Pools: views}
}

func swapbookPoolDetailData(values url.Values, now time.Time) poolDetailData {
	state := values.Get("state")
	var view poolView
	switch state {
	case "scaling":
		view = swapbookPool("pending", "Scaling", 0, 1, 1, now.Add(-40*time.Minute))
	case "atCapacity":
		view = swapbookPool("at-capacity", "At capacity", 0, 2, 2, now.Add(-2*time.Hour))
	case "degraded":
		view = swapbookPool("degraded", "Degraded", 0, 1, 2, now.Add(-3*time.Hour))
	default:
		view = swapbookPool("ready", "Ready", 1, 0, 1, now.Add(-time.Hour))
	}
	var active []sandboxView
	if swapbookQueryBool(values, "activeSandboxes", nil) {
		fixtures := []sandboxView{
			swapbookSandbox("sandbox-pool-1", "running", "python:3.12-slim", "1 core / 2 GiB", "default-pool-a1b2c", now.Add(-2*time.Minute)),
			swapbookSandbox("sandbox-running", "running", "python:3.12-slim", "1 core / 2 GiB", "sandbox-running-0", now.Add(-4*time.Minute)),
		}
		count := int(view.Allocated)
		if count == 0 {
			count = 1
			view.Allocated = 1
			view.Total = view.Available + view.Allocated
		}
		active = fixtures[:min(count, len(fixtures))]
	}
	return poolDetailData{poolView: view, PoolTotal: 1, SandboxTotal: 2, ActiveSandboxes: active}
}

func swapbookSandboxDetailData(values url.Values, now time.Time) sandboxDetailData {
	state := values.Get("state")
	if state != "pending" && state != "paused" && state != "failed" {
		state = "running"
	}
	fromPool := swapbookQueryBool(values, "fromPool", nil)
	id := "sandbox-running"
	pod := "sandbox-running-0"
	parentPool := ""
	poolRef := ""
	if fromPool {
		id = "sandbox-pool-1"
		pod = "default-pool-a1b2c"
		parentPool = "default-pool"
		poolRef = "default-pool"
	}
	view := swapbookSandbox(id, state, "python:3.12-slim", "1 core / 2 GiB", pod, now.Add(-4*time.Minute))
	data := swapbookSandboxDetail(view)
	data.ParentPool = parentPool
	data.PoolRef = poolRef
	return data
}

func swapbookQueryBool(values url.Values, name string, defaults map[string]bool) bool {
	raw, exists := values[name]
	if !exists || len(raw) == 0 {
		return defaults[name]
	}
	value, err := strconv.ParseBool(raw[0])
	return err == nil && value
}

func swapbookSandbox(id, state, image, resources, pod string, created time.Time) sandboxView {
	return sandboxView{
		ID: id, Name: id, State: state, StateLabel: sandboxStateLabel(state),
		CreatedAtISO: created.Format(time.RFC3339), CreatedAtFallback: created.Format(time.RFC822),
		Namespace: "opensandbox", PodName: pod, Image: image, Resources: resources,
	}
}

func swapbookSandboxDetail(view sandboxView) sandboxDetailData {
	return sandboxDetailData{
		ID: view.ID, Total: 1, PoolTotal: 1, State: view.State, StateLabel: view.StateLabel,
		CreatedAtISO: view.CreatedAtISO, CreatedAtFallback: view.CreatedAtFallback,
		Namespace: view.Namespace, PodName: view.PodName, Image: view.Image,
		Resources: view.Resources, Sources: "Lifecycle API + BatchSandbox", LifecycleManaged: true,
	}
}

func swapbookPool(state, label string, available, allocated, total int32, created time.Time) poolView {
	return poolView{
		Name: "default-pool", Namespace: "opensandbox", State: state, StateLabel: label,
		Image: "python:3.12-slim", Resources: "1 core / 2 GiB", RuntimeClass: "kata-vm-isolation",
		Available: available, Allocated: allocated, Total: total,
		PoolMin: 1, PoolMax: 2, BufferMin: 1, BufferMax: 2,
		CreatedAtISO: created.Format(time.RFC3339), CreatedAtFallback: created.Format(time.RFC822),
	}
}
