#!/usr/bin/env node
import { chromium } from 'playwright';

const base = process.argv[2] || 'http://localhost:8090';
const width = 430;
const height = 932;

const browser = await chromium.launch({ headless: true });
const page = await browser.newPage({ viewport: { width, height } });
await page.goto(base + '/');
await page.waitForSelector('table');

const headerRows = await page.evaluate(() => {
  const inner = document.querySelector('.site-header-inner');
  if (!inner) return { gridRows: 0, row1Links: 0, row2Links: 0 };
  const brand = inner.querySelector('.brand');
  const row1 = inner.querySelector('.site-nav-row1');
  const row2 = inner.querySelector('.site-nav-row2');
  const brandRow = brand ? brand.offsetTop : -1;
  const row1Top = row1 ? row1.offsetTop : -1;
  const row2Top = row2 ? row2.offsetTop : -1;
  return {
    brandRow,
    row1Top,
    row2Top,
    row1Links: row1 ? row1.querySelectorAll('a').length : 0,
    row2Links: row2 ? row2.querySelectorAll('a').length : 0,
    row1Wraps: row1 ? new Set([...row1.querySelectorAll('a')].map((a) => a.offsetTop)).size : 0,
    row2Wraps: row2 ? new Set([...row2.querySelectorAll('a')].map((a) => a.offsetTop)).size : 0,
  };
});

console.log('header layout', headerRows);

const layout = await page.evaluate(() => {
  const wrap = document.querySelector('.table-wrap');
  const table = document.querySelector('table');
  return {
    wrapScrollWidth: wrap?.scrollWidth,
    wrapClientWidth: wrap?.clientWidth,
    tableScrollWidth: table?.scrollWidth,
    bodyClientWidth: document.body.clientWidth,
    cols: document.querySelectorAll('table thead td[data-spot]').length,
  };
});

console.log('viewport', width);
console.log('table cols', layout.cols);
console.log('wrap', layout.wrapClientWidth, 'vs scroll', layout.wrapScrollWidth);

const overflow = layout.wrapScrollWidth > layout.wrapClientWidth + 2;
const headerOk =
  headerRows.row1Links === 5 &&
  headerRows.row2Links === 6 &&
  Math.abs(headerRows.brandRow - headerRows.row1Top) <= 12 &&
  headerRows.row2Top > headerRows.row1Top;

if (!headerOk) console.error('FAIL: header not in 2-row split layout', headerRows);
if (overflow) console.error('FAIL: table horizontal overflow');
if (headerOk && !overflow) console.log('OK: mobile layout fits');

await browser.close();
process.exitCode = overflow || !headerOk ? 1 : 0;
