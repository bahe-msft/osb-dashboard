//go:build swapbook

package dashboard

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"strconv"
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

	readyPool := swapbookPool("ready", "Ready", 1, 0, 1, now.Add(-time.Hour))
	capacityPool := swapbookPool("at-capacity", "At capacity", 0, 2, 2, now.Add(-time.Hour))
	readyPoolDetail := poolDetailData{poolView: readyPool, PoolTotal: 1, SandboxTotal: 1}
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

	reg.RegisterIn("pages", "Pool details",
		swapbookPageVariant("ready empty", index, pageData{SandboxImage: "python:3.12-slim", PoolName: "default-pool", Page: "pool-detail", ContentURL: "/dashboard/pools/default-pool/fragment"}, "GET /dashboard/pools/default-pool/fragment", swapbookRender(pool, readyPoolDetail)),
		swapbookPageVariant("active sandboxes", index, pageData{SandboxImage: "python:3.12-slim", PoolName: "default-pool", Page: "pool-detail", ContentURL: "/dashboard/pools/default-pool/fragment"}, "GET /dashboard/pools/default-pool/fragment", swapbookRender(pool, capacityPoolDetail)),
	)

	reg.RegisterIn("pages", "Snapshots",
		swapbookPageVariant("empty", index, pageData{SandboxImage: "python:3.12-slim", Page: "snapshots", ContentURL: "/dashboard/snapshots"}, "GET /dashboard/snapshots", swapbookRender(snapshots, emptySnapshots)),
		swapbookPageVariant("ready", index, pageData{SandboxImage: "python:3.12-slim", Page: "snapshots", ContentURL: "/dashboard/snapshots"}, "GET /dashboard/snapshots", swapbookRender(snapshots, readySnapshots)),
	)

	reg.RegisterIn("pages", "Cluster stats",
		swapbookPageVariant("with nodes", index, pageData{SandboxImage: "python:3.12-slim", Page: "stats", ContentURL: "/dashboard/stats"}, "GET /dashboard/stats", swapbookRender(stats, clusterStats)),
	)

	reg.RegisterIn("details", "Sandbox details",
		swapbookPageVariant("standalone", index, pageData{SandboxImage: "python:3.12-slim", SandboxID: running.ID, Page: "detail", ContentURL: "/dashboard/sandboxes/sandbox-running/fragment"}, "GET /dashboard/sandboxes/sandbox-running/fragment", swapbookRender(sandbox, sandboxDetail)),
		swapbookPageVariant("from pool", index, pageData{SandboxImage: "python:3.12-slim", SandboxID: pooled.ID, ParentPool: "default-pool", Page: "detail", ContentURL: "/dashboard/sandboxes/sandbox-pool-1/fragment?pool=default-pool"}, "GET /dashboard/sandboxes/sandbox-pool-1/fragment?pool=default-pool", swapbookRender(sandbox, pooledDetail)),
	)

	assetsFS, err := fs.Sub(webFiles, "web/assets")
	if err != nil {
		return nil, fmt.Errorf("load Swapbook assets: %w", err)
	}
	mux := http.NewServeMux()
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
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(assetsFS))))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok\n") })
	return mux, nil
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
