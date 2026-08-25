# Repository instructions

## Browser automation

- Use `playwright-cli` for browser-based checks and screenshots.
- Use a Chromium-based browser for browser automation, such as Chromium, Google Chrome, or Microsoft Edge. Pass the corresponding `--browser` value when opening a session; the repository configuration defaults to Chromium.
- Put all temporary Playwright artifacts in `.playwright/`, including screenshots, snapshots, console and network logs, traces, PDFs, and videos. For explicit output files, use paths such as `--filename .playwright/<name>.png`.
- Do not create temporary Playwright artifacts in the repository root or in `.playwright-cli/`.
- Keep `.playwright/cli.config.json` tracked. Other files under `.playwright/` are temporary and ignored by Git.
- After every UI change, run the application, capture an updated screenshot in `.playwright/` with a Chromium-based browser, inspect it, and include the screenshot preview in the response.
- Close Playwright sessions after use.
