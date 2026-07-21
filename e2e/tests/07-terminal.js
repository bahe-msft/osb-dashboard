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
  async function selectPanel(name) {
    await page.locator('.sandbox-detail-context-header [data-sandbox-info-toggle]').click();
    await page.getByRole('menuitemradio', { name, exact: true }).click();
    await waitFor(`${name} panel`, () => page.evaluate(expected => window.osbSandboxInfoPanel === expected, name.toLowerCase()));
  }

  await page.getByRole('button', { name: 'Connect', exact: true }).click();
  await waitFor('open terminal WebSocket', () => page.evaluate(() => Boolean(
    window.osbTerminalSocket &&
    window.osbTerminalSocket.readyState === WebSocket.OPEN &&
    window.osbTerminalActive &&
    document.querySelector('[data-terminal-overlay]')?.hidden
  )));

  const marker = `osb-e2e-${config.runID}`;
  const output = await page.evaluate(async expected => {
    const socket = window.osbTerminalSocket;
    const encoder = new TextEncoder();
    const decoder = new TextDecoder();
    let collected = '';
    return new Promise((resolve, reject) => {
      const timeout = setTimeout(() => {
        socket.removeEventListener('message', onMessage);
        reject(new Error(`terminal output did not contain ${expected}; received ${collected}`));
      }, 20_000);
      function onMessage(event) {
        if (typeof event.data === 'string') { return; }
        const message = new Uint8Array(event.data);
        if (message[0] !== 1 && message[0] !== 2) { return; }
        collected += decoder.decode(message.slice(1), { stream: true });
        if (collected.includes(expected)) {
          clearTimeout(timeout);
          socket.removeEventListener('message', onMessage);
          resolve(collected);
        }
      }
      socket.addEventListener('message', onMessage);
      const command = encoder.encode(`printf '${expected}\\n'\n`);
      const frame = new Uint8Array(command.length + 1);
      frame[0] = 0;
      frame.set(command, 1);
      socket.send(frame);
    });
  }, marker);
  if (!output.includes(marker)) { throw new Error('terminal command marker was not returned'); }

  await page.evaluate(() => { window.__osbE2ESocket = window.osbTerminalSocket; });
  await selectPanel('Stats');
  await page.locator('#sandbox-live-stats[data-stats-loaded="true"]').waitFor({ state: 'visible', timeout: 30_000 });
  await selectPanel('Details');
  await page.waitForTimeout(6_000);
  const preserved = await page.evaluate(() => Boolean(
    window.osbTerminalSocket === window.__osbE2ESocket &&
    window.osbTerminalSocket?.readyState === WebSocket.OPEN &&
    window.osbTerminalActive
  ));
  if (!preserved) { throw new Error('terminal WebSocket disconnected after switching panels'); }
  await page.screenshot({ path: `.playwright/e2e/${config.runID}/terminal-connected.png`, fullPage: true });

  return {
    category: 'Terminal',
    passed: 1,
    tests: ['terminal executes a command and survives panel switches'],
  };
}
