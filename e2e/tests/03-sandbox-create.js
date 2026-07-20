async page => {
  const baseURL = await page.evaluate(() => window.location.origin);
  const config = await page.evaluate(() => ({
    runID: localStorage.getItem('osb-e2e-run-id'),
    image: localStorage.getItem('osb-e2e-image') || 'python:3.12-slim',
  }));
  async function rowIDs() {
    return page.locator('[data-sandbox-id]').evaluateAll(rows =>
      rows.map(row => row.getAttribute('data-sandbox-id')).filter(Boolean)
    );
  }
  async function waitFor(description, check, timeout = 8 * 60_000) {
    const deadline = Date.now() + timeout;
    while (Date.now() < deadline) {
      const value = await check();
      if (value) { return value; }
      await page.waitForTimeout(500);
    }
    throw new Error(`timed out waiting for ${description}`);
  }

  await page.goto(`${baseURL}/`);
  await page.locator('[data-state-filter="all"]').waitFor({ state: 'visible', timeout: 30_000 });
  const before = new Set(await rowIDs());
  await page.locator('[data-open-create-modal]').first().click();
  await page.locator('#create-sandbox-modal[open]').waitFor({ state: 'visible' });
  await page.locator('#sandbox-image').fill(config.image);
  await page.getByRole('button', { name: 'Create', exact: true }).click();
  const sandboxID = await waitFor('new sandbox row', async () => {
    const current = await rowIDs();
    return current.find(id => !before.has(id)) || '';
  });
  await page.evaluate(id => localStorage.setItem('osb-e2e-sandbox-id', id), sandboxID);
  await waitFor('create dialog to become hidden', async () => !(await page.locator('#create-sandbox-modal').isVisible()), 5_000);
  if (await page.locator('#dashboard-content #dashboard-content').count()) {
    throw new Error('create response nested dashboard-content instead of replacing it');
  }
  await page.screenshot({ path: `.playwright/e2e/${config.runID}/sandbox-created.png`, fullPage: true });
  return {
    category: 'Sandbox lifecycle',
    passed: 1,
    sandboxID,
    tests: ['create a sandbox from the dashboard'],
  };
}
