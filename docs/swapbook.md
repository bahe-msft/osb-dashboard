# Swapbook development workflow

Swapbook is the dashboard's development-only component workbench. It renders
production Go templates, CSS, JavaScript, and HTMX behavior against deterministic
fixture data, without requiring Kubernetes or an OpenSandbox lifecycle service.

Swapbook support is compiled only with the `swapbook` Go build tag and is not
included in normal dashboard binaries.

## Requirements

- Go
- `curl`
- `python3` for inspection report generation
- `playwright-cli` with a Chromium-based browser for inspections

The Swapbook version is pinned in `go.mod`.

## Interactive development

Run the workbench:

```bash
just swapbook
```

Open <http://127.0.0.1:7007/__sb/>. The launcher builds temporary concrete
binaries, starts the fixture target on `127.0.0.1:8081`, starts the Swapbook
proxy on port 7007, and removes both processes and binaries on exit.

Override the addresses when needed:

```bash
SWAPBOOK_TARGET_ADDR=127.0.0.1:9081 \
SWAPBOOK_PORT=9007 \
just swapbook
```

Use the interactive workbench while changing templates or styles to inspect
states that are difficult to produce in a live cluster. The sandbox and Pool
list stories expose state controls, while detail stories expose one state
selector plus context controls such as Pool ownership and active sandboxes.

Swapbook's `mock`, `safe`, and `live` buttons control interaction behavior. The
dashboard fixtures are intended for `mock` mode. Width buttons include the
custom `dashboard` and `compact` viewports.

## Render smoke check

To verify that every registered story renders:

```bash
# terminal 1
go run -tags swapbook ./cmd/osb-dashboard-swapbook

# terminal 2
go run github.com/Aejkatappaja/swapbook/cmd/swapbook@v0.5.0 \
  check --target http://127.0.0.1:8081
```

This is a reachability/render check. It does not run the dashboard inspection
assertions or inspect screenshots.

## Visual and semantic inspection

Run the complete inspection matrix:

```bash
just swapbook-inspect
```

The command starts isolated target and gallery ports, reads the code-defined
inspection specification, and captures every case at both `dashboard` and
`compact` widths. It exits non-zero when a code assertion or browser-console
check fails.

Artifacts are stored under:

```text
.playwright/swapbook-inspection/<run-id>/
```

The run directory contains:

- `report.html` — visual report with screenshots and expandable code evidence
- `report.json` — machine-readable run summary and assertion results
- `spec.json` — the exact inspection specification used by the run
- `llm-review.md` — review instructions for an LLM
- `swapbook.log` — target and proxy logs
- one directory per case and viewport containing:
  - `visual.png`
  - `semantic.aria.yml`
  - `rendered.html`
  - `console.log`
  - `network.log`
  - `assert.js`

Open `report.html` in a browser or serve the run directory with a local static
server. Pair `report.json`, the PNG, the semantic snapshot, and the source paths
when asking an LLM to inspect a case.

## Inspection specification

The specification lives next to the stories in `swapbook.go` in
`swapbookInspectionSpec()`. Keeping it in Go means story fixtures, control
arguments, source mappings, and expected behavior are reviewed together.

Each case declares:

- stable case ID
- Swapbook story and variant
- control arguments
- viewports
- relevant source files
- declarative assertions

Supported assertion types are:

| Type | Meaning |
| --- | --- |
| `visible` | selector exists and its first match is visible |
| `text` | combined matching text contains the expected value |
| `count` | selector match count equals the expected integer |
| `attribute` | first match has the expected attribute value |
| `no-overflow` | selected element's scroll width does not exceed its client width |

Every case also receives a page-level horizontal-overflow check and a browser
console check.

## Adding or changing dashboard UI

When adding a page, component, or meaningful state:

1. Register or update its story in `NewSwapbookHandler()`.
2. Prefer controls over separate variants when states can be composed.
3. Add deterministic fixture handlers for controlled HTMX requests.
4. Mock interaction routes, including query strings when HTMX sends them.
5. Add or update an inspection case in `swapbookInspectionSpec()`.
6. Include the production template, shared component, CSS, and JavaScript paths
   in the case's `Sources` list.
7. Add objective assertions for structure and behavior; leave subjective visual
   judgment to the generated screenshot review.
8. Run `just swapbook-inspect` before handing off or opening a PR.

Do not create every possible control permutation. Add a focused scenario matrix
covering meaningful empty, healthy, transitional, capacity, failure, and
responsive states.

## Swapbook versus live-cluster E2E

Use Swapbook for deterministic rendering, responsive layout, component states,
and mocked HTMX behavior. Use the live E2E suite when behavior depends on real
Kubernetes resources, OpenSandbox lifecycle operations, terminal WebSockets,
snapshots, scheduling, or authentication.

A UI change may require both workflows: Swapbook for broad visual/state
coverage and live E2E for infrastructure-backed behavior.

## Troubleshooting

### A story remains on its loading skeleton

Inspect the network panel. The story's initial `hx-get` must either match a
Swapbook mock exactly or resolve to a fixture endpoint on the target. Query
strings are significant for htmx mocks.

### A launcher leaves a process behind

Use the provided scripts rather than wrapping both servers in `go run`. The
launcher builds temporary concrete binaries so signal handling owns the real
server PIDs.

### A story is missing

Swapbook does not scan templates automatically. Register the story explicitly
in `NewSwapbookHandler()` and verify `/_swapbook/manifest.json`.
