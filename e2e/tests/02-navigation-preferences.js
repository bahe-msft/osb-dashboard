async page => {
  async function waitFor(description, check, timeout = 30_000) {
    const deadline = Date.now() + timeout;
    while (Date.now() < deadline) {
      if (await check()) { return; }
      await page.waitForTimeout(100);
    }
    throw new Error(`timed out waiting for ${description}`);
  }

  const results = [];
  const navIcon = page.locator('.nav-item .nav-item-icon').first();
  const before = await navIcon.boundingBox();
  if (!before) { throw new Error('navigation icon is not visible before collapse'); }
  await page.locator('#sidebar-toggle').click();
  await waitFor('collapsed sidebar', async () => page.locator('html[data-sidebar="collapsed"]').count());
  await page.waitForTimeout(250);
  const after = await navIcon.boundingBox();
  if (!after || Math.abs(before.x - after.x) >= 1) {
    throw new Error(`navigation icon moved during collapse: ${before.x} -> ${after && after.x}`);
  }
  await page.reload();
  if (await page.locator('html[data-sidebar="collapsed"]').count() !== 1) {
    throw new Error('collapsed sidebar preference did not persist');
  }
  await page.locator('#workspace-mark').click();
  await waitFor('expanded sidebar', async () => page.locator('html[data-sidebar="expanded"]').count());
  results.push('sidebar collapse persists without moving icons');

  const initialTheme = await page.locator('html').getAttribute('data-theme');
  await page.locator('.nav-menu').hover();
  await page.locator('#theme-switch').waitFor({ state: 'visible' });
  await page.locator('#theme-switch').click();
  const expectedTheme = initialTheme === 'dim' ? 'corporate' : 'dim';
  await waitFor(`${expectedTheme} theme`, async () => (await page.locator('html').getAttribute('data-theme')) === expectedTheme);
  await page.reload();
  if (await page.locator('html').getAttribute('data-theme') !== expectedTheme) {
    throw new Error('theme preference did not persist');
  }
  await page.locator('.nav-menu').hover();
  await page.locator('#theme-switch').waitFor({ state: 'visible' });
  await page.locator('#theme-switch').click();
  results.push('theme selection persists across reload');

  return { category: 'Navigation and preferences', passed: results.length, tests: results };
}
