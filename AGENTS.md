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
