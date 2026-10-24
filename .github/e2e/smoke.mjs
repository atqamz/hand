import fs from 'node:fs';
import { createRequire } from 'node:module';
import path from 'node:path';
import { fixture } from './fixture.mjs';
import { Box, candidate, check, equal, logDir, pass, phase, soft, startBoard, stopAll, tmpRoot } from './lib.mjs';

const playwright = createRequire(import.meta.url)(process.env.E2E_PLAYWRIGHT ?? path.join(tmpRoot, 'pw', 'node_modules', 'playwright'));
const shots = path.join(logDir, 'shots');
const widths = [1440, 390];

async function visit(browser, link, id, width) {
  const tag = `${process.platform}-${width}`;
  const context = await browser.newContext({ viewport: { width, height: width > 600 ? 900 : 844 }, deviceScaleFactor: 1 });
  const page = await context.newPage();
  const problems = [];
  page.on('console', (m) => m.type() === 'error' && problems.push(`console: ${m.text()}`));
  page.on('pageerror', (e) => problems.push(`page error: ${e.message}`));
  const csp = [];
  await page.exposeFunction('reportCsp', (v) => csp.push(v));
  await page.addInitScript(() => {
    document.addEventListener('securitypolicyviolation', (e) => window.reportCsp(`${e.violatedDirective} ${e.blockedURI}`));
  });
  try {
    await page.goto(link.href, { waitUntil: 'domcontentloaded' });
    await page.waitForSelector('[data-region="status"]', { state: 'attached' });
    equal(new URL(page.url()).pathname, `/${id}/`, `${tag}: the login link lands on the fleet page`);
    check(!page.url().includes('token='), `${tag}: the token is gone from the address bar`);
    for (const region of ['status', 'console', 'timeline', 'queue', 'tasks']) equal(await page.locator(`[data-region="${region}"]`).count(), 1, `${tag}: the ${region} region is on the page`);
    equal(await page.locator('#composer').evaluate((el) => el.closest('[data-region]') === null), true, `${tag}: the composer sits outside every region`);

    if (width < 900) await page.locator('nav.tabs a[data-tab="tasks"]').click();
    const tasks = page.locator('[data-region="tasks"]');
    await tasks.waitFor({ state: 'visible' });
    const badges = await tasks.locator('.badge.project').allTextContents();
    equal([...new Set(badges.map((b) => b.trim()))].sort(), ['alpha', 'beta'], `${tag}: the Tasks region shows a project badge for each of the two projects`);
    check(await tasks.getByText('Add a CSV export').first().isVisible(), `${tag}: the Tasks region lists the active task`);
    await page.screenshot({ path: path.join(shots, `${tag}-fleet.png`) });

    const chip = tasks.locator('a[href*="/doc/docs/export-notes.md"]').first();
    await chip.waitFor({ state: 'visible' });
    await chip.click();
    const dialog = page.locator('dialog.viewer[open]');
    await dialog.waitFor({ state: 'visible' });
    await dialog.getByRole('heading', { name: 'Export notes' }).waitFor({ state: 'visible' });
    pass(`${tag}: the document opens in a dialog over the page`);
    check(new URL(page.url()).pathname.endsWith('/doc/docs/export-notes.md'), `${tag}: the address bar shows the file while the dialog is open`);
    await page.screenshot({ path: path.join(shots, `${tag}-dialog.png`) });
    await page.keyboard.press('Escape');
    await page.locator('dialog.viewer[open]').waitFor({ state: 'detached', timeout: 5000 }).catch(() => {});
    equal(await page.locator('dialog.viewer[open]').count(), 0, `${tag}: Escape closes the dialog`);
    await page.waitForFunction((p) => location.pathname === p, `/${id}/`);
    const focused = await page.evaluate(() => `${document.activeElement.tagName} ${document.activeElement.getAttribute('href') ?? ''} ${location.hash}`);
    const returned = focused.startsWith('A ') && focused.includes('/doc/docs/export-notes.md');
    if (width >= 900) check(returned, `${tag}: focus returns to a link to the document (${focused})`);
    else soft(returned, `${tag}: focus returns to a link to the document (${focused}); the address carries a fragment and Back moves focus to the page`);

    await page.goto(new URL(`/${id}/doc/docs/export-notes.md`, link.origin).href, { waitUntil: 'domcontentloaded' });
    await page.getByRole('heading', { name: 'Export notes' }).waitFor({ state: 'visible' });
    pass(`${tag}: the document page renders the Markdown`);
    check(await page.locator('table').first().isVisible(), `${tag}: the document page renders the table`);
    await page.screenshot({ path: path.join(shots, `${tag}-doc.png`) });

    equal(csp, [], `${tag}: no CSP violation on the fleet page, the dialog or the document page`);
    equal(problems, [], `${tag}: no console error or page error`);
  } finally {
    await context.close();
  }
}

await phase('smoke', async () => {
  await candidate();
  const box = new Box('first');
  if (!fs.existsSync(path.join(box.fleet, 'hand.db'))) {
    box.prepare();
    await fixture(box);
  }
  fs.mkdirSync(shots, { recursive: true });
  const link = await startBoard(box);
  const id = link.pathname.split('/')[1];
  const browser = await playwright.chromium.launch();
  try {
    check(true, `chromium ${browser.version()} on ${process.platform}`);
    for (const width of widths) await visit(browser, link, id, width);
  } finally {
    await browser.close();
    await stopAll(box);
  }
});
