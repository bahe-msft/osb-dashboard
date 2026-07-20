async page => {
  const current = await page.evaluate(() => {
    const url = new URL(window.location.href);
    return {
      baseURL: url.origin,
      runID: url.searchParams.get('e2eRun') || `manual-${Date.now()}`,
      image: url.searchParams.get('e2eImage') || 'python:3.12-slim',
    };
  });
  await page.evaluate(config => {
    localStorage.clear();
    localStorage.setItem('osb-e2e-run-id', config.runID);
    localStorage.setItem('osb-e2e-image', config.image);
  }, current);
  await page.reload();

  const health = await page.request.get(`${current.baseURL}/healthz`);
  if (!health.ok() || (await health.text()).trim() !== 'ok') {
    throw new Error(`health endpoint failed with HTTP ${health.status()}`);
  }
  await page.locator('[data-state-filter="all"]').waitFor({ state: 'visible', timeout: 30_000 });
  if (!await page.getByRole('link', { name: /Sandboxes/ }).first().isVisible()) {
    throw new Error('Sandboxes navigation is not visible');
  }
  return {
    category: 'Application shell',
    passed: 1,
    tests: ['health endpoint and overview render'],
  };
}
