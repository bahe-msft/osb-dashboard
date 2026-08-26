# Repository instructions

## Browser automation

- Use `playwright-cli` for browser-based checks and screenshots.
- Use a Chromium-based browser for browser automation, such as Chromium, Google Chrome, or Microsoft Edge. Pass the corresponding `--browser` value when opening a session; the repository configuration defaults to Chromium.
- Put all temporary Playwright artifacts in `.playwright/`, including screenshots, snapshots, console and network logs, traces, PDFs, and videos. For explicit output files, use paths such as `--filename .playwright/<name>.png`.
- Do not create temporary Playwright artifacts in the repository root or in `.playwright-cli/`.
- Keep `.playwright/cli.config.json` tracked. Other files under `.playwright/` are temporary and ignored by Git.
- After every UI change, run the application, capture an updated screenshot in `.playwright/` with a Chromium-based browser, inspect it, and include the screenshot preview in the response.
- Perform a minimal accessibility pass for UI changes: inspect the semantic Playwright snapshot, confirm controls have accessible names and appropriate roles, verify keyboard focus where relevant, and check for obvious landmark or heading issues.
- Perform a minimal interaction-style pass: pointer cursors and hover treatments must be reserved for interactive elements, while focus, disabled, loading, copy, and default cursor states must match their semantics.
- Check changed layouts at desktop width and at least one narrow viewport for obvious clipping, overlap, or unusable controls.
- Close Playwright sessions after use.

## Swapbook development workflow

- Read `docs/swapbook.md` before adding or changing Swapbook stories, controls, fixture routes, inspection cases, or report generation.
- Use `just swapbook` while developing server-rendered UI states that do not require Kubernetes. Prefer it for template work, shared row/component changes, empty/loading/error states, Pool and sandbox state combinations, responsive layout, and mocked HTMX interactions.
- Keep stories explicit; Swapbook does not scan templates automatically. Prefer controls over separate variants when states can be composed, and keep fixture data deterministic.
- When adding a page, component, or meaningful UI state, update both its Swapbook registration and the code-defined inspection matrix in `swapbookInspectionSpec()`. Include relevant production source paths and objective assertions.
- Run `just swapbook-inspect` before completing a change that affects dashboard templates, shared UI components, navigation, responsive behavior, state presentation, or common CSS. Inspect `report.html` and report the pass/fail summary and artifact path.
- A copy-only or backend-only change does not require the full inspection matrix unless it affects rendered structure or behavior. The normal Go tests still apply.
- Use Swapbook for deterministic visual/state coverage. Use live-cluster E2E for Kubernetes discovery, lifecycle mutations, scheduling, authentication, snapshots, terminal WebSockets, or other infrastructure-backed behavior. Run both when a UI change spans deterministic rendering and live infrastructure.
- Keep generated inspection artifacts under `.playwright/swapbook-inspection/`; do not commit them.
