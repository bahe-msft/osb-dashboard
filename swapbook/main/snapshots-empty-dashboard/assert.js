async page => {
  await page.waitForLoadState('domcontentloaded');
  await page.waitForFunction(() => !document.querySelector('[aria-label="Loading dashboard content"]'), null, { timeout: 15000 });
  await page.waitForTimeout(250);
  const assertions = [{"type": "visible", "selector": ".snapshot-empty-state"}, {"type": "count", "selector": ".snapshot-row", "expected": 0}];
  const results = [];
  for (const assertion of assertions) {
    const locator = page.locator(assertion.selector);
    const count = await locator.count();
    let actual;
    let passed = false;
    if (assertion.type === 'visible') {
      actual = count > 0 && await locator.first().isVisible();
      passed = actual === true;
    } else if (assertion.type === 'text') {
      actual = count ? (await locator.allTextContents()).join(' ').replace(/\s+/g, ' ').trim() : '';
      passed = actual.includes(String(assertion.expected));
    } else if (assertion.type === 'count') {
      actual = count;
      passed = actual === Number(assertion.expected);
    } else if (assertion.type === 'attribute') {
      actual = count ? await locator.first().getAttribute(assertion.attribute) : null;
      passed = count > 0 && actual === String(assertion.expected);
    } else if (assertion.type === 'no-overflow') {
      actual = count ? await locator.first().evaluate(el => ({ scrollWidth: el.scrollWidth, clientWidth: el.clientWidth })) : null;
      passed = actual !== null && actual.scrollWidth <= actual.clientWidth;
    } else {
      actual = 'unknown assertion type';
    }
    const result = { ...assertion, actual, passed };
    results.push(result);
    if (!passed) throw new Error(`inspection assertion failed: ${JSON.stringify(result)}`);
  }
  const pageOverflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth);
  if (pageOverflow) throw new Error('inspection assertion failed: page has horizontal overflow');
  return { title: await page.title(), url: page.url(), pageOverflow, assertions: results };
}
