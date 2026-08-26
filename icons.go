package dashboard

// dashboardIconTarget maps one dashboard concept to its intentional Lucide
// icon. Targets, rather than glyph names, are the source of truth so repeated
// icons (for example Sandbox, Namespace, and Pod) remain visible and can be
// reviewed independently.
type dashboardIconTarget struct {
	Target   string
	Icon     string
	Category string
	Purpose  string
}

var dashboardIconCatalog = []dashboardIconTarget{
	// Navigation and layout.
	{Target: "Sandbox", Icon: "box", Category: "navigation", Purpose: "Sandboxes navigation and sandbox resources"},
	{Target: "Pool", Icon: "boxes", Category: "navigation", Purpose: "Pools navigation and Pool references"},
	{Target: "Snapshot", Icon: "file-box", Category: "navigation", Purpose: "Snapshots navigation and snapshot actions"},
	{Target: "Cluster statistics", Icon: "chart-no-axes-combined", Category: "navigation", Purpose: "Cluster statistics navigation"},
	{Target: "Settings", Icon: "settings", Category: "navigation", Purpose: "Settings navigation"},
	{Target: "Search", Icon: "search", Category: "navigation", Purpose: "Search affordance"},
	{Target: "Expand sidebar", Icon: "panel-left-open", Category: "navigation", Purpose: "Expand the primary sidebar"},
	{Target: "Collapse sidebar", Icon: "panel-left-close", Category: "navigation", Purpose: "Collapse the primary sidebar"},
	{Target: "Show details pane", Icon: "panel-right-open", Category: "navigation", Purpose: "Show the sandbox details pane"},
	{Target: "Hide details pane", Icon: "panel-right-close", Category: "navigation", Purpose: "Hide the sandbox details pane"},
	{Target: "Open submenu", Icon: "chevron-right", Category: "navigation", Purpose: "Open the settings submenu"},
	{Target: "Expand group", Icon: "chevron-down", Category: "navigation", Purpose: "Expand groups and selector menus"},
	{Target: "Live updates enabled", Icon: "refresh-cw", Category: "navigation", Purpose: "Live updates enabled"},
	{Target: "Live updates paused", Icon: "refresh-cw-off", Category: "navigation", Purpose: "Live updates paused"},
	{Target: "Dark theme", Icon: "moon", Category: "navigation", Purpose: "Dark theme endpoint"},
	{Target: "Light theme", Icon: "sun", Category: "navigation", Purpose: "Light theme endpoint"},

	// Actions.
	{Target: "Deploy sandbox", Icon: "plus", Category: "action", Purpose: "Create or deploy a sandbox"},
	{Target: "Close modal", Icon: "x", Category: "action", Purpose: "Close a modal"},
	{Target: "Pause sandbox", Icon: "pause", Category: "action", Purpose: "Pause a sandbox"},
	{Target: "Resume sandbox", Icon: "play", Category: "action", Purpose: "Resume a sandbox"},
	{Target: "More actions", Icon: "ellipsis-vertical", Category: "action", Purpose: "Open more actions"},
	{Target: "Delete sandbox", Icon: "circle-x", Category: "action", Purpose: "Delete or remove a sandbox"},
	{Target: "Delete snapshot", Icon: "trash-2", Category: "action", Purpose: "Delete a snapshot"},
	{Target: "Restore from snapshot", Icon: "archive-restore", Category: "action", Purpose: "Restore a sandbox from a snapshot"},
	{Target: "Acquire from Pool", Icon: "archive-restore", Category: "action", Purpose: "Acquire a sandbox from a Pool"},

	// Status and feedback.
	{Target: "Error", Icon: "circle-alert", Category: "status", Purpose: "Error state"},
	{Target: "Destructive confirmation", Icon: "circle-alert", Category: "status", Purpose: "Destructive confirmation"},
	{Target: "Successful operation", Icon: "circle-check", Category: "status", Purpose: "Successful operation"},
	{Target: "Kubernetes warning event", Icon: "triangle-alert", Category: "status", Purpose: "Kubernetes warning event"},
	{Target: "Pending operation", Icon: "loader-circle", Category: "status", Purpose: "Pending operation spinner"},
	{Target: "Resource status", Icon: "circle-dot", Category: "status", Purpose: "Resource status property"},

	// Kubernetes and resource properties.
	{Target: "Source sandbox", Icon: "box", Category: "resource", Purpose: "Source sandbox reference"},
	{Target: "Kubernetes namespace", Icon: "square-chart-gantt", Category: "resource", Purpose: "Namespace containing the workload"},
	{Target: "Kubernetes Pod", Icon: "container", Category: "resource", Purpose: "Pod backing the sandbox"},
	{Target: "Container image", Icon: "disc", Category: "resource", Purpose: "Container image"},
	{Target: "CPU", Icon: "cpu", Category: "resource", Purpose: "CPU usage"},
	{Target: "Resource allocation", Icon: "cpu", Category: "resource", Purpose: "Requested sandbox resources"},
	{Target: "Memory", Icon: "memory-stick", Category: "resource", Purpose: "Memory usage"},
	{Target: "Load", Icon: "gauge", Category: "resource", Purpose: "One-minute load metric"},
	{Target: "Cluster nodes", Icon: "server", Category: "resource", Purpose: "Cluster node count"},
	{Target: "No scheduled nodes", Icon: "server-off", Category: "resource", Purpose: "No scheduled node workload"},
	{Target: "Sandbox load distribution", Icon: "trending-up", Category: "resource", Purpose: "Sandbox load distribution"},
	{Target: "Created time", Icon: "calendar-clock", Category: "resource", Purpose: "Creation timestamp"},
	{Target: "Last transition", Icon: "history", Category: "resource", Purpose: "Last state transition"},
	{Target: "Discovery source", Icon: "waypoints", Category: "resource", Purpose: "Kubernetes and lifecycle discovery sources"},
	{Target: "Runtime class", Icon: "monitor-cloud", Category: "resource", Purpose: "Sandbox runtime class"},
	{Target: "Pool available", Icon: "package-check", Category: "resource", Purpose: "Available Pool capacity"},
	{Target: "Pool allocated", Icon: "boxes", Category: "resource", Purpose: "Allocated Pool sandboxes"},
	{Target: "Pool total", Icon: "boxes", Category: "resource", Purpose: "Total Pool capacity"},
	{Target: "Pool minimum and maximum", Icon: "arrow-up-1-0", Category: "resource", Purpose: "Pool minimum and maximum"},
	{Target: "Warm Pool buffer", Icon: "replace-all", Category: "resource", Purpose: "Warm Pool buffer"},

	// Information panels and empty states.
	{Target: "Details panel", Icon: "info", Category: "interface", Purpose: "Details panel"},
	{Target: "Statistics panel", Icon: "square-activity", Category: "interface", Purpose: "Live statistics panel"},
	{Target: "Events panel", Icon: "list-tree", Category: "interface", Purpose: "Events panel"},
	{Target: "Metadata panel", Icon: "braces", Category: "interface", Purpose: "Metadata panel"},
	{Target: "Filtered empty state", Icon: "list-filter", Category: "interface", Purpose: "Filtered list empty state"},
	{Target: "Events available", Icon: "list-checks", Category: "interface", Purpose: "Events available empty state"},
	{Target: "Events unavailable", Icon: "circle-off", Category: "interface", Purpose: "Events unavailable empty state"},
	{Target: "No active Pool sandboxes", Icon: "monitor-off", Category: "interface", Purpose: "No active Pool sandboxes"},
	{Target: "Web terminal", Icon: "terminal-square", Category: "interface", Purpose: "Web terminal state"},
}
