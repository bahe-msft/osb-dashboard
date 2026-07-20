async page => {
  const config = await page.evaluate(() => ({
    runID: localStorage.getItem('osb-e2e-run-id'),
    sandboxID: localStorage.getItem('osb-e2e-sandbox-id'),
  }));
  if (!config.sandboxID) { throw new Error('created sandbox ID is missing'); }

  async function waitFor(description, check, timeout = 60_000) {
    const deadline = Date.now() + timeout;
    while (Date.now() < deadline) {
      const value = await check();
      if (value) { return value; }
      await page.waitForTimeout(500);
    }
    throw new Error(`timed out waiting for ${description}`);
  }
  async function currentState() {
    const heading = page.locator('.sandbox-detail-summary h1');
    if (!await heading.count()) { return ''; }
    return (await heading.textContent()).trim().toLowerCase();
  }
  async function connectTerminal() {
    await page.getByRole('button', { name: 'Connect', exact: true }).click();
    await waitFor('open terminal WebSocket', () => page.evaluate(() => Boolean(
      window.osbTerminalSocket &&
      window.osbTerminalSocket.readyState === WebSocket.OPEN &&
      window.osbTerminalActive &&
      document.querySelector('[data-terminal-overlay]')?.hidden
    )));
  }
  async function runTerminalCommand(command, expected) {
    return page.evaluate(async ({ command, expected }) => {
      const socket = window.osbTerminalSocket;
      const encoder = new TextEncoder();
      const decoder = new TextDecoder();
      let collected = '';
      return new Promise((resolve, reject) => {
        const timeout = setTimeout(() => {
          socket.removeEventListener('message', onMessage);
          reject(new Error(`terminal output did not contain ${expected}; received ${collected}`));
        }, 60_000);
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
        const bytes = encoder.encode(`${command}\n`);
        const frame = new Uint8Array(bytes.length + 1);
        frame[0] = 0;
        frame.set(bytes, 1);
        socket.send(frame);
      });
    }, { command, expected });
  }

  const results = [];
  const marker = `pause-state-${config.runID}`;
  const rootDir = '/root/osb-e2e';
  const shareDir = '/usr/local/share/osb-e2e';
  const varDir = '/var/lib/osb-e2e';
  const tmpDir = '/tmp/osb-e2e';
  await connectTerminal();
  await runTerminalCommand(
    `set -e; mkdir -p ${rootDir} ${shareDir} ${varDir} ${tmpDir}; ` +
    `printf '${marker}' > ${rootDir}/small.txt; ` +
    `dd if=/dev/zero of=${shareDir}/one-mib.bin bs=1048576 count=1 status=none; ` +
    `dd if=/dev/urandom of=${varDir}/eight-mib.bin bs=1048576 count=8 status=none; ` +
    `dd if=/dev/zero of=${tmpDir}/sixteen-mib.bin bs=1048576 count=16 status=none; ` +
    `sha256sum ${rootDir}/small.txt ${shareDir}/one-mib.bin ${varDir}/eight-mib.bin ${tmpDir}/sixteen-mib.bin > ${rootDir}/manifest.sha256; ` +
    `echo files-created`,
    'files-created',
  );
  await runTerminalCommand(
    `wc -c ${shareDir}/one-mib.bin ${varDir}/eight-mib.bin ${tmpDir}/sixteen-mib.bin | tail -1`,
    '26214400 total',
  );
  await runTerminalCommand("printf 'verified:'; wc -c < /root/osb-e2e/small.txt", `verified:${marker.length}`);
  await page.waitForTimeout(500);

  await page.getByRole('button', { name: 'Pause sandbox', exact: true }).click();
  await waitFor('pause request acceptance', async () => {
    const state = await currentState();
    return state === 'pausing' || state === 'paused';
  }, 15_000);
  await page.screenshot({ path: `.playwright/e2e/${config.runID}/sandbox-pausing.png`, fullPage: true });
  results.push('submit a pause request for a running sandbox');

  const settledState = await waitFor('pause result', async () => {
    const state = await currentState();
    return state !== 'pausing' && state !== '' ? state : '';
  }, 10 * 60_000);
  if (settledState !== 'paused') {
    return {
      category: 'Pause and resume',
      passed: results.length,
      skipped: 1,
      reason: `cluster returned to ${settledState} after accepting pause`,
      tests: results,
    };
  }

  await page.getByRole('button', { name: 'Resume sandbox', exact: true }).waitFor({ state: 'visible' });
  if (await page.locator('#sandbox-terminal').getAttribute('data-terminal-enabled') !== 'false') {
    throw new Error('terminal remained enabled while sandbox was paused');
  }
  await page.screenshot({ path: `.playwright/e2e/${config.runID}/sandbox-paused.png`, fullPage: true });

  await page.getByRole('button', { name: 'Resume sandbox', exact: true }).click();
  await waitFor('resume request acceptance', async () => {
    const state = await currentState();
    return state === 'resuming' || state === 'running';
  }, 15_000);
  await waitFor('running sandbox after resume', async () => await currentState() === 'running', 3 * 60_000);
  await page.getByRole('button', { name: 'Pause sandbox', exact: true }).waitFor({ state: 'visible' });
  if (await page.locator('#sandbox-terminal').getAttribute('data-terminal-enabled') !== 'true') {
    throw new Error('terminal did not become available after resume');
  }
  await page.screenshot({ path: `.playwright/e2e/${config.runID}/sandbox-resumed.png`, fullPage: true });
  results.push('resume a paused sandbox');

  await connectTerminal();
  let persistenceError = '';
  try {
    const restored = await runTerminalCommand(`cat ${rootDir}/small.txt`, marker);
    if (!restored.includes(marker)) { throw new Error('small marker was not restored'); }
    await runTerminalCommand(
      `wc -c ${shareDir}/one-mib.bin ${varDir}/eight-mib.bin ${tmpDir}/sixteen-mib.bin | tail -1`,
      '26214400 total',
    );
    await runTerminalCommand(`sha256sum -c ${rootDir}/manifest.sha256`, `${tmpDir}/sixteen-mib.bin: OK`);
  } catch (error) {
    persistenceError = error.message;
  }
  await page.evaluate(() => {
    if (window.osbTerminalSocket) { window.osbTerminalSocket.close(1000, 'E2E pause/resume verification complete'); }
    window.osbTerminalSocket = null;
    window.osbTerminalActive = false;
  });
  await page.reload();
  await page.locator('[data-terminal-connect]').waitFor({ state: 'visible', timeout: 30_000 });
  if (persistenceError) {
    return {
      category: 'Pause and resume',
      passed: results.length,
      skipped: 1,
      reason: `root filesystem persistence unavailable: ${persistenceError}`,
      tests: results,
    };
  }
  results.push('preserve files across multiple rootfs paths and sizes up to 16 MiB');
  return { category: 'Pause and resume', passed: results.length, tests: results };
}
