# Dashboard iconography

The dashboard uses [Lucide](https://lucide.dev/) through the vendored
`web/assets/third-party/ui/lucide.min.js` bundle. `icons.go` is the canonical
machine-readable mapping from **dashboard target** to icon. Targets are listed
separately even when they currently share a glyph, so ambiguous mappings such
as Sandbox, Kubernetes Namespace, and Kubernetes Pod remain visible and can be
updated independently.

The **Iconography** Swapbook story renders every target mapping, and
`icons_test.go` prevents templates or JavaScript from using an undocumented
icon.

## Usage rules

- Choose icons by semantic target, not by searching for a visually similar
  glyph at each call site.
- Use only mappings listed in `icons.go`. Add a separate target when a new
  concept happens to reuse an existing icon.
- Icons that decorate text or a labelled control must use `aria-hidden="true"`.
  The containing button or link must provide its own visible text or accessible
  name.
- Do not use icon names as accessible labels. Name the action or resource, not
  the glyph.
- Keep standard interface icons at the existing 14–16 px sizes. Property and
  empty-state contexts may use their component-specific sizes from
  `web/assets/styles.css`.
- Use `loader-circle` only with the shared rotation treatment for pending work.
- Resource state dots are CSS `.status-ring` elements, not Lucide icons.
- Dynamic icon names in `web/assets/app.js` must also exist in the catalog.
- When changing a mapping, update `icons.go` and every production use for that
  target. The Swapbook catalog and inspection count update from `icons.go`.

## Target catalog

### Navigation and layout

| Target | Icon | Purpose |
| --- | --- | --- |
| Sandbox | `box` | Sandboxes navigation and sandbox resources |
| Pool | `boxes` | Pools navigation and Pool references |
| Snapshot | `file-box` | Snapshots navigation and snapshot actions |
| Cluster statistics | `chart-no-axes-combined` | Cluster statistics navigation |
| Settings | `settings` | Settings navigation |
| Search | `search` | Search affordance |
| Expand sidebar | `panel-left-open` | Expand the primary sidebar |
| Collapse sidebar | `panel-left-close` | Collapse the primary sidebar |
| Show details pane | `panel-right-open` | Show the sandbox details pane |
| Hide details pane | `panel-right-close` | Hide the sandbox details pane |
| Open submenu | `chevron-right` | Open the settings submenu |
| Expand group | `chevron-down` | Expand groups and selector menus |
| Live updates enabled | `refresh-cw` | Live updates enabled |
| Live updates paused | `refresh-cw-off` | Live updates paused |
| Dark theme | `moon` | Dark theme endpoint |
| Light theme | `sun` | Light theme endpoint |

### Actions

| Target | Icon | Purpose |
| --- | --- | --- |
| Deploy sandbox | `plus` | Create or deploy a sandbox |
| Close modal | `x` | Close a modal |
| Pause sandbox | `pause` | Pause a sandbox |
| Resume sandbox | `play` | Resume a sandbox |
| More actions | `ellipsis-vertical` | Open more actions |
| Delete sandbox | `circle-x` | Delete or remove a sandbox |
| Delete snapshot | `trash-2` | Delete a snapshot |
| Restore from snapshot | `archive-restore` | Restore a sandbox from a snapshot |
| Acquire from Pool | `archive-restore` | Acquire a sandbox from a Pool |

### Status and feedback

| Target | Icon | Purpose |
| --- | --- | --- |
| Error | `circle-alert` | Error state |
| Destructive confirmation | `circle-alert` | Destructive confirmation |
| Successful operation | `circle-check` | Successful operation |
| Kubernetes warning event | `triangle-alert` | Kubernetes warning event |
| Pending operation | `loader-circle` | Pending operation spinner |
| Resource status | `circle-dot` | Resource status property |

### Kubernetes and resource properties

| Target | Icon | Purpose |
| --- | --- | --- |
| Source sandbox | `box` | Source sandbox reference |
| Kubernetes namespace | `square-chart-gantt` | Namespace containing the workload |
| Kubernetes Pod | `container` | Pod backing the sandbox |
| Container image | `disc` | Container image |
| CPU | `cpu` | CPU usage |
| Resource allocation | `cpu` | Requested sandbox resources |
| Memory | `memory-stick` | Memory usage |
| Load | `gauge` | One-minute load metric |
| Cluster nodes | `server` | Cluster node count |
| No scheduled nodes | `server-off` | No scheduled node workload |
| Sandbox load distribution | `trending-up` | Sandbox load distribution |
| Created time | `calendar-clock` | Creation timestamp |
| Last transition | `history` | Last state transition |
| Discovery source | `waypoints` | Kubernetes and lifecycle discovery sources |
| Runtime class | `monitor-cloud` | Sandbox runtime class |
| Pool available | `package-check` | Available Pool capacity |
| Pool allocated | `boxes` | Allocated Pool sandboxes |
| Pool total | `boxes` | Total Pool capacity |
| Pool minimum and maximum | `arrow-up-1-0` | Pool minimum and maximum |
| Warm Pool buffer | `replace-all` | Warm Pool buffer |

### Information panels and empty states

| Target | Icon | Purpose |
| --- | --- | --- |
| Details panel | `info` | Details panel |
| Statistics panel | `square-activity` | Live statistics panel |
| Events panel | `list-tree` | Events panel |
| Metadata panel | `braces` | Metadata panel |
| Filtered empty state | `list-filter` | Filtered list empty state |
| Events available | `list-checks` | Events available empty state |
| Events unavailable | `circle-off` | Events unavailable empty state |
| No active Pool sandboxes | `monitor-off` | No active Pool sandboxes |
| Web terminal | `terminal-square` | Web terminal state |

## Review workflow

Run the interactive catalog:

```bash
just swapbook
```

Select **Iconography · catalog**. Cards are labelled by dashboard target and
show the underlying Lucide icon name beneath the target.

Before completing icon changes, run:

```bash
just swapbook-inspect
```

The `iconography` inspection case verifies that every target mapping renders to
SVG at both dashboard and compact widths and that Lucide emits no browser
warnings.
