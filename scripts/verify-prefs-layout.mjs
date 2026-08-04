#!/usr/bin/env node
/**
 * Verify index table layout follows ikite.prefs (order + display).
 * Usage: node scripts/verify-prefs-layout.mjs [baseUrl]
 */
import { chromium } from 'playwright';

const base = process.argv[2] || 'http://localhost:8090';

async function getSpotHeaders(page) {
  return page.$$eval('table thead tr td[data-spot]', (cells) =>
    cells.map((td) => td.getAttribute('data-spot'))
  );
}

async function main() {
  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage();

  // 1) Defaults from server (no localStorage)
  await page.goto(base + '/prefs');
  await page.evaluate(() => localStorage.removeItem('ikite.prefs'));
  await page.reload();
  await page.waitForSelector('#collect-body tr[data-key]');

  const prefsChecks = await page.$$eval('#collect-body tr[data-key]', (rows) =>
    rows.map((row) => ({
      key: row.dataset.key,
      checked: row.querySelector('.spot-display').checked,
    }))
  );
  const checkedCount = prefsChecks.filter((r) => r.checked).length;
  console.log('prefs default checked count:', checkedCount, 'of', prefsChecks.length);

  // 2) Index without prefs should match server display defaults
  await page.goto(base + '/');
  let headers = await getSpotHeaders(page);
  console.log('index headers (no prefs):', headers.join(', '));
  console.log('index column count (no prefs):', headers.length);

  // 3) Reorder on prefs: move first row down once
  await page.goto(base + '/prefs');
  await page.waitForSelector('#collect-body tr[data-key]');
  const firstKey = await page.$eval('#collect-body tr[data-key]', (r) => r.dataset.key);
  await page.click('#collect-body tr[data-key] .move-down');
  await page.waitForTimeout(500);

  const orderAfter = await page.$$eval('#collect-body tr[data-key]', (rows) =>
    rows.map((r) => r.dataset.key)
  );
  console.log('prefs order after move-down on first:', orderAfter.slice(0, 4).join(', '));

  // 4) Index should follow new order (visible spots only)
  await page.goto(base + '/');
  headers = await getSpotHeaders(page);
  console.log('index headers after reorder:', headers.slice(0, 4).join(', '));

  const expectedFirst = orderAfter.find((k) => {
    const row = prefsChecks.find((p) => p.key === k);
    return row && row.checked;
  });
  if (headers[0] !== expectedFirst) {
    console.error('FAIL: expected first visible column', expectedFirst, 'got', headers[0]);
    process.exitCode = 1;
  } else {
    console.log('OK: first column matches prefs order');
  }

  // 5) Uncheck first visible spot and verify hidden on index
  await page.goto(base + '/prefs');
  await page.click('#collect-body tr[data-key="' + firstKey + '"] .spot-display');
  await page.waitForTimeout(500);
  await page.goto(base + '/');
  headers = await getSpotHeaders(page);
  if (headers.includes(firstKey)) {
    console.error('FAIL: unchecked spot still on index:', firstKey);
    process.exitCode = 1;
  } else {
    console.log('OK: unchecked spot hidden on index');
  }

  await browser.close();
  if (!process.exitCode) console.log('All checks passed');
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
