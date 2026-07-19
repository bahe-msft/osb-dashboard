# Repository instructions

## Browser automation

- Use `playwright-cli` for browser-based checks and screenshots.
- Use Microsoft Edge only. Pass `--browser msedge` when opening a session; the repository configuration also pins Playwright's Chromium browser to the `msedge` channel.
- Put all temporary Playwright artifacts in `.playwright/`, including screenshots, snapshots, console and network logs, traces, PDFs, and videos. For explicit output files, use paths such as `--filename .playwright/<name>.png`.
- Do not create temporary Playwright artifacts in the repository root or in `.playwright-cli/`.
- Keep `.playwright/cli.config.json` tracked. Other files under `.playwright/` are temporary and ignored by Git.
- After every UI change, run the application, capture an updated screenshot in `.playwright/` with Microsoft Edge, inspect it, and include the screenshot preview in the response.
- Close Playwright sessions after use.
