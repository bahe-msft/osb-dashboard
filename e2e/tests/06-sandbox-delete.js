async page => {
  const config = await page.evaluate(() => ({
    runID: localStorage.getItem('osb-e2e-run-id'),
    sandboxID: localStorage.getItem('osb-e2e-sandbox-id'),
  }));
  if (!config.sandboxID) { throw new Error('created sandbox ID is missing'); }

  async function waitFor(description, check, timeout = 60_000) {
    const deadline = Date.now() + timeout;
    while (Date.now() < deadline) {
      if (await check()) { return; }
      await page.waitForTimeout(250);
    }
    throw new Error(`timed out waiting for ${description}`);
  }
  async function rowIDs() {
    return page.locator('[data-sandbox-id]').evaluateAll(rows =>
      rows.map(row => row.getAttribute('data-sandbox-id')).filter(Boolean)
    );
  }

  await page.locator('[data-sandbox-actions-toggle]').click();
  await page.getByRole('menuitem', { name: 'Delete', exact: true }).click();
  await page.locator('#confirmation-modal[open]').waitFor({ state: 'visible' });
  await page.locator('#confirmation-modal-submit').click();
  await waitFor('return to overview', () => page.evaluate(() => window.location.pathname === '/'));
  await waitFor('sandbox removal', async () => !(await rowIDs()).includes(config.sandboxID));
  await page.evaluate(() => localStorage.removeItem('osb-e2e-sandbox-id'));
  await page.screenshot({ path: `.playwright/e2e/${config.runID}/suite-complete.png`, fullPage: true });

  return {
    category: 'Sandbox lifecycle',
    passed: 1,
    tests: ['delete the test sandbox through the dashboard'],
  };
}
