async page => {
  const current = await page.evaluate(() => {
    const url = new URL(window.location.href);
    return {
      baseURL: url.origin,
      runID: url.searchParams.get('e2eRun') || `manual-${Date.now()}`,
      image: url.searchParams.get('e2eImage') || 'python:3.12-slim',
      keepSandbox: url.searchParams.get('e2eKeepSandbox') === '1',
    };
  });
  await page.evaluate(config => {
    localStorage.clear();
    localStorage.setItem('osb-e2e-run-id', config.runID);
    localStorage.setItem('osb-e2e-image', config.image);
    localStorage.setItem('osb-e2e-keep-sandbox', config.keepSandbox ? '1' : '0');
  }, current);
  await page.reload();

  const health = await page.request.get(`${current.baseURL}/healthz`);
  if (!health.ok() || (await health.text()).trim() !== 'ok') {
    throw new Error(`health endpoint failed with HTTP ${health.status()}`);
  }
  const embeddedAssets = [
    { path: '/assets/third-party/ui/htmx.min.js', contentType: 'text/javascript' },
    { path: '/assets/third-party/ui/basecoat.min.css', contentType: 'text/css' },
    { path: '/assets/third-party/ghostty-web/ghostty-web.js', contentType: 'text/javascript' },
    { path: '/assets/third-party/ghostty-web/ghostty-vt.wasm', contentType: 'application/wasm' },
  ];
  for (const asset of embeddedAssets) {
    const response = await page.request.get(`${current.baseURL}${asset.path}`);
    const actualType = response.headers()['content-type'] || '';
    if (!response.ok() || !actualType.startsWith(asset.contentType)) {
      throw new Error(`${asset.path} returned HTTP ${response.status()} with Content-Type ${actualType || '<missing>'}`);
    }
  }
  await page.locator('[data-state-filter="all"]').waitFor({ state: 'visible', timeout: 30_000 });
  if (!await page.getByRole('link', { name: /Sandboxes/ }).first().isVisible()) {
    throw new Error('Sandboxes navigation is not visible');
  }
  await page.getByRole('link', { name: /Snapshots/ }).click();
  await page.locator('#dashboard-content[data-page="snapshots"]').waitFor({ state: 'visible' });
  if (!await page.getByRole('button', { name: /All/ }).isVisible()) {
    throw new Error('Snapshots page did not render');
  }
  await page.getByRole('link', { name: 'Stats', exact: true }).click();
  await page.locator('#dashboard-content[data-page="stats"]').waitFor({ state: 'visible' });
  if (await page.locator('.cluster-stat-card').count() !== 3) {
    throw new Error('Stats summary did not render');
  }
  await page.getByRole('link', { name: /Sandboxes/ }).first().click();
  await page.locator('#dashboard-content[data-page="list"]').waitFor({ state: 'visible' });
  return {
    category: 'Application shell',
    passed: 4,
    tests: ['health endpoint and embedded assets', 'overview render', 'snapshots navigation and page render', 'cluster stats navigation and summary render'],
  };
}
