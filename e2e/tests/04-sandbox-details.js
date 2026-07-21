async page => {
  const baseURL = await page.evaluate(() => window.location.origin);
  const config = await page.evaluate(() => ({
    runID: localStorage.getItem('osb-e2e-run-id'),
    sandboxID: localStorage.getItem('osb-e2e-sandbox-id'),
  }));
  if (!config.sandboxID) { throw new Error('created sandbox ID is missing'); }

  async function waitFor(description, check, timeout = 30_000, interval = 250) {
    const deadline = Date.now() + timeout;
    while (Date.now() < deadline) {
      if (await check()) { return; }
      await page.waitForTimeout(interval);
    }
    throw new Error(`timed out waiting for ${description}`);
  }
  async function selectPanel(name) {
    await page.locator('.sandbox-detail-context-header [data-sandbox-info-toggle]').click();
    await page.getByRole('menuitemradio', { name, exact: true }).click();
    await waitFor(`${name} panel`, () => page.evaluate(expected => window.osbSandboxInfoPanel === expected, name.toLowerCase()));
  }

  const results = [];
  await page.goto(`${baseURL}/dashboard/sandboxes/${encodeURIComponent(config.sandboxID)}`);
  await page.evaluate(() => {
    window.osbLiveUpdatesEnabled = true;
    localStorage.setItem('opensandbox-live-updates', 'enabled');
  });
  await waitFor('running sandbox detail', async () => {
    const heading = page.locator('.sandbox-detail-summary h1');
    return (await heading.count()) && (await heading.textContent()).trim().toLowerCase() === 'running';
  }, 8 * 60_000, 1_000);
  if (await page.locator('#sandbox-terminal').getAttribute('data-sandbox-id') !== config.sandboxID) {
    throw new Error('terminal points at the wrong sandbox');
  }
  if (await page.locator('[data-sandbox-info-content="details"] .sandbox-info-property').count() < 4) {
    throw new Error('detail properties did not render');
  }
  results.push('created sandbox becomes running and opens its details');

  let statsResponses = 0;
  const countStats = response => {
    if (response.url().includes(`/dashboard/sandboxes/${encodeURIComponent(config.sandboxID)}/stats`) && response.ok()) {
      statsResponses += 1;
    }
  };
  page.on('response', countStats);
  try {
    await selectPanel('Stats');
    await page.locator('#sandbox-live-stats[data-stats-loaded="true"]').waitFor({ state: 'visible', timeout: 30_000 });
    const values = await page.locator('.sandbox-info-property-value strong').allTextContents();
    if (!/^\d+(?:\.\d+)?%$/.test(values[0].trim())) {
      throw new Error(`unexpected CPU value ${values[0]}`);
    }
    if (!/\/\s*(?:unlimited|\d+(?:\.\d+)?\s+[KMGT]iB)$/.test(values[2].trim())) {
      throw new Error(`unexpected memory value ${values[2]}`);
    }
    await waitFor('second stats response', async () => statsResponses >= 2, 20_000, 500);
    if (await page.locator('.sandbox-usage-meter').count() < 2) {
      throw new Error('utilization indicators are missing');
    }
  } finally {
    page.off('response', countStats);
  }
  await page.screenshot({ path: `.playwright/e2e/${config.runID}/live-stats.png`, fullPage: true });
  results.push('live stats load and refresh from the sandbox');

  await selectPanel('Events');
  await page.locator('#sandbox-events[data-events-loaded="true"]').waitFor({ state: 'visible', timeout: 30_000 });
  if (!await page.locator('.sandbox-event, .sandbox-events-empty').count()) {
    throw new Error('pod events panel did not render a result');
  }
  await page.screenshot({ path: `.playwright/e2e/${config.runID}/pod-events.png`, fullPage: true });
  results.push('pod events load independently from the terminal workspace');
  await selectPanel('Details');

  return { category: 'Sandbox details', passed: results.length, tests: results };
}
