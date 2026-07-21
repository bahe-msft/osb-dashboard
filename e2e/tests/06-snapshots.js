async page => {
  const baseURL = await page.evaluate(() => window.location.origin);
  const config = await page.evaluate(() => ({
    runID: localStorage.getItem('osb-e2e-run-id'),
    sandboxID: localStorage.getItem('osb-e2e-sandbox-id'),
  }));
  if (!config.sandboxID) { throw new Error('created sandbox ID is missing'); }

  async function waitFor(description, check, timeout = 60_000, interval = 500) {
    const deadline = Date.now() + timeout;
    while (Date.now() < deadline) {
      const value = await check();
      if (value) { return value; }
      await page.waitForTimeout(interval);
    }
    throw new Error(`timed out waiting for ${description}`);
  }
  async function cleanup(path) {
    let lastError;
    for (let attempt = 1; attempt <= 3; attempt += 1) {
      try {
        const response = await page.request.delete(`${baseURL}${path}`, {
          headers: { 'X-OSB-CSRF': '1' },
        });
        if (response.ok()) { return; }
        lastError = new Error(`cleanup ${path} failed with HTTP ${response.status()}: ${await response.text()}`);
      } catch (error) {
        lastError = error;
      }
      await page.waitForTimeout(attempt * 500);
    }
    throw lastError;
  }

  const results = [];
  const snapshotName = `e2e-${config.runID}`;
  let snapshotID = '';
  let deployedSandboxID = '';
  let testError;

  try {
    await page.goto(`${baseURL}/dashboard/sandboxes/${encodeURIComponent(config.sandboxID)}`);
    await page.evaluate(() => {
      window.osbLiveUpdatesEnabled = false;
      localStorage.setItem('opensandbox-live-updates', 'paused');
    });
    await page.getByRole('button', { name: 'Create snapshot', exact: true }).click();
    await page.locator('#create-snapshot-modal[open]').waitFor({ state: 'visible' });

    const sourceTag = await page.locator('#snapshot-source-sandbox').evaluate(element => element.tagName);
    if (sourceTag !== 'OUTPUT') { throw new Error(`snapshot source uses ${sourceTag}, want OUTPUT`); }
    const nameInput = page.locator('#snapshot-name');
    const autocomplete = await nameInput.evaluate(element => ({
      autocomplete: element.autocomplete,
      onePasswordIgnore: element.hasAttribute('data-1p-ignore'),
      lastPassIgnore: element.getAttribute('data-lpignore'),
    }));
    if (autocomplete.autocomplete !== 'off' || !autocomplete.onePasswordIgnore || autocomplete.lastPassIgnore !== 'true') {
      throw new Error(`snapshot name autocomplete is not disabled: ${JSON.stringify(autocomplete)}`);
    }
    await nameInput.fill(snapshotName);
    await page.locator('#create-snapshot-form button[type="submit"]').click();

    if (await page.evaluate(() => window.location.pathname) !== `/dashboard/sandboxes/${config.sandboxID}`) {
      throw new Error('snapshot creation navigated away from the sandbox detail page');
    }
    const creating = page.locator('#snapshot-create-result-content[data-snapshot-state="creating"]');
    await creating.waitFor({ state: 'visible', timeout: 30_000 });
    const creatingUI = await creating.evaluate(element => ({
      polling: element.getAttribute('hx-trigger'),
      target: element.getAttribute('hx-target'),
      spinnerAnimation: getComputedStyle(document.querySelector('#create-snapshot-modal-icon svg')).animationName,
      bodyIcons: element.querySelectorAll('.snapshot-create-result-icon').length,
      progressBars: element.querySelectorAll('.snapshot-create-progress').length,
      detailBlocks: element.querySelectorAll('dl').length,
      reference: element.querySelector('.operation-resource-reference')?.textContent.trim().replace(/\s+/g, ' '),
      backgroundAction: element.querySelector('footer button')?.textContent.trim(),
    }));
    if (creatingUI.polling !== 'load delay:2s' || creatingUI.target !== 'this' || creatingUI.spinnerAnimation === 'none') {
      throw new Error(`snapshot polling UI is not active: ${JSON.stringify(creatingUI)}`);
    }
    if (creatingUI.bodyIcons !== 0 || creatingUI.progressBars !== 0 || creatingUI.detailBlocks !== 0 || creatingUI.reference !== `Snapshot: ${snapshotName}` || creatingUI.backgroundAction !== 'Run in background') {
      throw new Error(`snapshot modal contains redundant progress/detail UI: ${JSON.stringify(creatingUI)}`);
    }
    snapshotID = await creating.getAttribute('data-snapshot-id');
    if (!snapshotID) { throw new Error('snapshot ID is missing from creation result'); }
    await page.screenshot({ path: `.playwright/e2e/${config.runID}/snapshot-creating.png`, fullPage: true });
    results.push('create a snapshot without leaving the sandbox page and show animated polling');

    const ready = page.locator('#snapshot-create-result-content[data-snapshot-state="ready"]');
    await ready.waitFor({ state: 'visible', timeout: 10 * 60_000 });
    if (await ready.getAttribute('hx-trigger')) { throw new Error('snapshot modal continued polling after Ready'); }
    const snapshotFooterButtons = await ready.locator('footer button').allTextContents();
    if (snapshotFooterButtons.map(value => value.trim()).join(',') !== 'View snapshot,Done') {
      throw new Error(`unexpected snapshot result actions: ${snapshotFooterButtons}`);
    }
    if (await page.getByRole('button', { name: 'Done', exact: true }).getAttribute('data-variant') !== 'primary') {
      throw new Error('Done is not the primary snapshot result action');
    }
    await page.screenshot({ path: `.playwright/e2e/${config.runID}/snapshot-ready.png`, fullPage: true });
    results.push('poll snapshot creation to Ready and stop the animation');

    await page.getByRole('button', { name: 'View snapshot', exact: true }).click();
    await waitFor('snapshot details navigation', () => page.evaluate(
      expected => window.location.pathname === `/snapshots/${expected}`,
      snapshotID,
    ));
    await page.locator('#dashboard-content[data-page="snapshot-detail"]').waitFor({ state: 'visible' });
    const detailLayout = await page.evaluate(() => {
      const panel = document.querySelector('.snapshot-detail-panel').getBoundingClientRect();
      const body = document.querySelector('.snapshot-detail-body').getBoundingClientRect();
      return {
        widthRatio: panel.width / body.width,
        messageVisible: Boolean(document.querySelector('.snapshot-detail-message')),
      };
    });
    if (detailLayout.widthRatio < 0.9 || detailLayout.messageVisible) {
      throw new Error(`unexpected snapshot detail layout: ${JSON.stringify(detailLayout)}`);
    }
    await page.locator('#navigation-title a').click();
    await page.locator('#dashboard-content[data-page="snapshots"]').waitFor({ state: 'visible' });
    const snapshotRow = page.locator(`article.snapshot-row[data-snapshot-id="${snapshotID}"]`);
    if (await snapshotRow.locator('.snapshot-id.copyable-value, .snapshot-source .copyable-value').count()) {
      throw new Error('snapshot list IDs remain copyable');
    }
    await snapshotRow.locator('.status-ring').click();
    await waitFor('whole-row snapshot navigation', () => page.evaluate(
      expected => window.location.pathname === `/snapshots/${expected}`,
      snapshotID,
    ));
    results.push('open the full-width minimal details page from the whole snapshot row');

    await page.getByRole('button', { name: `Deploy snapshot ${snapshotName}`, exact: true }).click();
    await page.locator('#create-sandbox-modal[open]').waitFor({ state: 'visible' });
    if ((await page.locator('#create-sandbox-modal-title').textContent()).trim() !== 'Deploy snapshot') {
      throw new Error('snapshot deployment modal title is incorrect');
    }
    if ((await page.locator('#create-sandbox-form .create-button-label').textContent()).trim() !== 'Deploy') {
      throw new Error('snapshot deployment submit label is incorrect');
    }
    if (!await page.locator('#create-sandbox-modal [data-create-sandbox-icon] svg').evaluate(
      icon => icon.classList.contains('lucide-archive-restore'),
    )) {
      throw new Error('snapshot deployment does not use the archive-restore icon');
    }
    await page.locator('#create-sandbox-form button[type="submit"]').click();

    if (await page.evaluate(() => window.location.pathname) !== `/snapshots/${snapshotID}`) {
      throw new Error('snapshot deployment navigated away from snapshot details');
    }
    const deploying = page.locator('#sandbox-deployment-result-content');
    await deploying.waitFor({ state: 'visible', timeout: 30_000 });
    deployedSandboxID = await deploying.getAttribute('data-sandbox-id');
    if (!deployedSandboxID) { throw new Error('deployed sandbox ID is missing'); }
    const deploymentUI = await deploying.evaluate(element => ({
      state: element.dataset.sandboxState,
      polling: element.getAttribute('hx-trigger'),
      target: element.getAttribute('hx-target'),
      spinnerAnimation: getComputedStyle(document.querySelector('#create-sandbox-modal-icon svg')).animationName,
      bodyIcons: element.querySelectorAll('.snapshot-create-result-icon').length,
      progressBars: element.querySelectorAll('.snapshot-create-progress').length,
      detailBlocks: element.querySelectorAll('dl').length,
      sandboxIDVisible: element.textContent.includes(element.dataset.sandboxId),
      backgroundAction: element.querySelector('footer button')?.textContent.trim(),
    }));
    if (!['pending', 'checking'].includes(deploymentUI.state) || deploymentUI.polling !== 'load delay:2s' || deploymentUI.target !== 'this' || deploymentUI.spinnerAnimation === 'none' || deploymentUI.bodyIcons !== 0 || deploymentUI.progressBars !== 0 || deploymentUI.detailBlocks !== 0 || deploymentUI.sandboxIDVisible || deploymentUI.backgroundAction !== 'Run in background') {
      throw new Error(`sandbox deployment polling UI is invalid: ${JSON.stringify(deploymentUI)}`);
    }
    await page.screenshot({ path: `.playwright/e2e/${config.runID}/snapshot-deploying.png`, fullPage: true });
    results.push('deploy from a snapshot without leaving snapshot details and show animated polling');

    const running = page.locator('#sandbox-deployment-result-content[data-sandbox-state="running"]');
    await running.waitFor({ state: 'visible', timeout: 8 * 60_000 });
    if (await running.getAttribute('hx-trigger')) { throw new Error('deployment modal continued polling after Running'); }
    const viewSandbox = page.getByRole('button', { name: 'View sandbox', exact: true });
    if (await viewSandbox.getAttribute('data-variant') !== 'primary') {
      throw new Error('View sandbox is not the primary deployment action');
    }
    await page.screenshot({ path: `.playwright/e2e/${config.runID}/snapshot-deployed.png`, fullPage: true });
    await viewSandbox.click();
    await waitFor('deployed sandbox details', () => page.evaluate(
      expected => window.location.pathname === `/dashboard/sandboxes/${expected}`,
      deployedSandboxID,
    ));
    results.push('poll deployment to Running and explicitly open the deployed sandbox');
  } catch (error) {
    testError = error;
  } finally {
    const cleanupErrors = [];
    if (deployedSandboxID) {
      try {
        await cleanup(`/dashboard/sandboxes/${encodeURIComponent(deployedSandboxID)}`);
      } catch (error) {
        cleanupErrors.push(error.message);
      }
    }
    if (snapshotID) {
      try {
        await cleanup(`/dashboard/snapshots/${encodeURIComponent(snapshotID)}`);
      } catch (error) {
        cleanupErrors.push(error.message);
      }
    }
    await page.goto(`${baseURL}/dashboard/sandboxes/${encodeURIComponent(config.sandboxID)}`);
    await page.evaluate(() => {
      window.osbLiveUpdatesEnabled = true;
      localStorage.setItem('opensandbox-live-updates', 'enabled');
    });
    if (cleanupErrors.length) {
      throw new Error(`snapshot E2E cleanup failed: ${cleanupErrors.join('; ')}`);
    }
  }

  if (testError) { throw testError; }
  return { category: 'Snapshots', passed: results.length, tests: results };
}
