import { spawn, spawnSync } from 'node:child_process';
import fs from 'node:fs';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';
import { createRequire } from 'node:module';

const repo = path.resolve(process.env.RENDER_REPO ?? path.join(import.meta.dirname, '..', '..'));
const out = path.resolve(process.env.RENDER_OUT ?? path.join(os.tmpdir(), 'hand-render'));
const asserting = process.env.RENDER_ASSERT !== '0';
const broken = process.env.RENDER_BREAK === '1';
const playwright = createRequire(import.meta.url)(process.env.RENDER_PLAYWRIGHT ?? 'playwright');

const frozen = Date.UTC(2026, 10, 1, 5, 0, 0);
const widths = [1440, 1024, 390];
const themes = ['light', 'dark'];
const sizes = { 1440: [1440, 900], 1024: [1024, 768], 390: [390, 844] };

const fleet = (n) => `f00000000e${n}/`;
const only = process.env.RENDER_ONLY?.split(',');
const scenes = [
  ['empty', fleet('001')],
  ['ready', fleet('002')],
  ['working', fleet('003')],
  ['blocked', fleet('004')],
  ['stopped', fleet('005')],
  ['many-tasks', fleet('007')],
  ['read-only', fleet('008')],
  ['long-task', fleet('00e')],
  ['deliverables', fleet('00f')],
  ['busy', fleet('010')],
  ['decisions', fleet('011')],
  ['documents', fleet('012')],
  ['ask', fleet('013')],
  ['cleanup', fleet('014')],
  ['menu', fleet('010'), ['details.nav-menu > summary'], 600],
  ['dialog', fleet('012'), ['?nav.tabs a[data-tab="tasks"]', '[data-region="tasks"] a[href*="/task/"]']],
  ['document', `${fleet('012')}doc/reports/t287/design.md`],
  ['log', `${fleet('010')}supervisor/log`],
  ['task', `${fleet('010')}task/t1`],
  ['attempt', `${fleet('010')}attempt/a1`],
].filter(([name]) => !only || only.includes(name));

function measure() {
  const ctx = document.createElement('canvas').getContext('2d', { willReadFrequently: true });
  const px = (css) => {
    ctx.clearRect(0, 0, 1, 1);
    ctx.fillStyle = '#000';
    ctx.fillStyle = css;
    ctx.fillRect(0, 0, 1, 1);
    const d = ctx.getImageData(0, 0, 1, 1).data;
    return [d[0], d[1], d[2], d[3] / 255];
  };
  const over = (t, b) => [0, 1, 2].map((i) => t[i] * t[3] + b[i] * (1 - t[3])).concat([1]);
  const lum = (c) => {
    const f = (v) => {
      v /= 255;
      return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
    };
    return 0.2126 * f(c[0]) + 0.7152 * f(c[1]) + 0.0722 * f(c[2]);
  };
  const ratio = (a, b) => {
    const [x, y] = [lum(a), lum(b)].sort((p, q) => q - p);
    return (x + 0.05) / (y + 0.05);
  };
  const bgOf = (el) => {
    const layers = [];
    for (let e = el; e; e = e.parentElement) layers.push(px(getComputedStyle(e).backgroundColor));
    return layers.reduceRight((acc, x) => over(x, acc), [255, 255, 255, 1]);
  };
  const low = [];
  const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
  for (let t = walker.nextNode(); t; t = walker.nextNode()) {
    const el = t.parentElement;
    if (!t.textContent.trim() || !el.getClientRects().length || el.closest('.sr,[hidden],script,style,meter')) continue;
    const cs = getComputedStyle(el);
    if (cs.visibility === 'hidden' || +cs.opacity === 0) continue;
    const bg = bgOf(el);
    const r = ratio(over(px(cs.color), bg), bg);
    if (r < 4.5) low.push({ ratio: Math.round(r * 100) / 100, text: t.textContent.trim().slice(0, 30) });
  }
  const visible = (e) => e.getClientRects().length && getComputedStyle(e).visibility !== 'hidden';
  const coarse = matchMedia('(pointer:coarse)').matches;
  const small = coarse
    ? [...document.querySelectorAll('button, summary, select, input:not([type=hidden]):not([type=file])')]
        .filter(visible)
        .map((e) => ({ h: Math.round(e.getBoundingClientRect().height * 10) / 10, what: (e.getAttribute('aria-label') || e.textContent || e.name || `${e.tagName.toLowerCase()}.${e.className}`).trim().slice(0, 40) }))
        .filter((c) => c.h < 44)
    : [];
  const outside = [...document.querySelectorAll('details[open] > .menu-body, details[open] > [role=menu], dialog[open], [role=menu]')]
    .filter(visible)
    .map((e) => ({ what: String(e.className || e.tagName).slice(0, 24), r: e.getBoundingClientRect() }))
    .filter(({ r }) => r.left < -0.5 || r.top < -0.5 || r.right > innerWidth + 0.5 || r.bottom > innerHeight + 0.5)
    .map(({ what, r }) => ({ what, box: [r.left, r.top, r.right, r.bottom].map(Math.round) }));
  const items = [...document.querySelectorAll('.timeline > .card, .timeline > .hand-line')];
  const when = (e) => new Date((e.matches('.wakes') ? e.querySelector('summary time:last-of-type') : e.querySelector('time[datetime]'))?.dateTime);
  const zone = [];
  items.forEach((e, i) => {
    const gap = e.nextElementSibling?.matches('.day') ? e.nextElementSibling : null;
    const split = i + 1 < items.length && when(e).toDateString() !== when(items[i + 1]).toDateString();
    if (!!gap !== split) zone.push(`${split ? 'no divider' : `divider ${JSON.stringify(gap.textContent.trim())}`} between two cards of ${split ? 'different' : 'the same'} local days at ${when(e).toString().slice(0, 21)}`);
    else if (gap && !new RegExp(`\\b${when(e).getDate()}\\b`).test(gap.textContent)) zone.push(`divider ${JSON.stringify(gap.textContent.trim())} is not the local day ${when(e).getDate()} of the card above`);
  });
  return {
    overflow: document.documentElement.scrollWidth - innerWidth,
    dividers: document.querySelectorAll('.timeline > .day').length,
    zone,
    outside,
    small,
    low: low.slice(0, 5),
    lowCount: low.length,
  };
}

function freePort() {
  return new Promise((resolve, reject) => {
    const srv = net.createServer();
    srv.once('error', reject);
    srv.listen(0, '127.0.0.1', () => {
      const { port } = srv.address();
      srv.close(() => resolve(port));
    });
  });
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function serve() {
  fs.mkdirSync(out, { recursive: true });
  const exe = path.join(out, process.platform === 'win32' ? 'board.test.exe' : 'board.test');
  const built = spawnSync('go', ['test', '-c', '-o', exe, './internal/board'], { cwd: repo, stdio: 'inherit' });
  if (built.status !== 0) throw new Error('go test -c failed');
  const ready = path.join(out, 'ready');
  const stop = path.join(out, 'stop');
  fs.rmSync(ready, { force: true });
  fs.rmSync(stop, { force: true });
  const addr = `127.0.0.1:${await freePort()}`;
  const logPath = path.join(out, 'server.log');
  const log = fs.openSync(logPath, 'w');
  const home = fs.mkdtempSync(path.join(os.tmpdir(), 'hand-render-home-'));
  const child = spawn(exe, ['-test.run=^TestRenderScenes$', '-test.timeout=30m', '-test.v'], {
    cwd: path.join(repo, 'internal', 'board'),
    env: { ...process.env, SECONDHAND_HOME: home, SCENES_ADDR: addr, SCENES_READY: ready, SCENES_STOP: stop },
    stdio: ['ignore', log, log],
  });
  const exited = new Promise((resolve) => child.once('exit', resolve));
  let gone = false;
  exited.then(() => (gone = true));
  for (let i = 0; i < 600 && !fs.existsSync(ready); i++) {
    if (gone) throw new Error(`the scene server exited early:\n${fs.readFileSync(logPath, 'utf8').slice(-2000)}`);
    await sleep(200);
  }
  if (!fs.existsSync(ready)) throw new Error('the scene server did not start in 120 s');
  const token = fs.readFileSync(ready, 'utf8').trim();
  const close = async () => {
    fs.writeFileSync(stop, '');
    await Promise.race([exited, sleep(10000)]);
    if (!gone) child.kill();
    fs.rmSync(home, { recursive: true, force: true });
  };
  return { base: `http://${addr}`, token, close };
}

async function open(page, url) {
  for (let attempt = 1; ; attempt++) {
    try {
      return await page.goto(url, { waitUntil: 'domcontentloaded' });
    } catch (e) {
      if (attempt === 5 || !String(e).includes('ERR_NO_BUFFER_SPACE')) throw e;
      await sleep(2000);
    }
  }
}

async function render(browser, server, width, theme, rows) {
  const [w, h] = sizes[width];
  const touch = width < 600;
  const context = await browser.newContext({
    viewport: { width: w, height: h },
    colorScheme: theme,
    reducedMotion: 'reduce',
    isMobile: touch,
    hasTouch: touch,
    deviceScaleFactor: 1,
    locale: 'en-US',
    timezoneId: 'Asia/Jakarta',
    serviceWorkers: 'block',
  });
  await context.clock.setFixedTime(frozen);
  await context.addInitScript(() => {
    window.__csp = [];
    document.addEventListener('securitypolicyviolation', (e) => window.__csp.push(`${e.violatedDirective} ${e.blockedURI}`));
  });
  const page = await context.newPage();
  const errors = [];
  page.on('console', (m) => m.type() === 'error' && errors.push(m.text().slice(0, 160)));
  page.on('pageerror', (e) => errors.push(String(e).slice(0, 160)));
  for (const [name, route, clicks = [], below = Infinity] of scenes) {
    if (width >= below) continue;
    errors.length = 0;
    const res = await open(page, `${server.base}/${route}?token=${server.token}`);
    await page.waitForTimeout(400);
    const missed = [];
    for (const raw of clicks) {
      const optional = raw.startsWith('?');
      try {
        await page.locator(optional ? raw.slice(1) : raw).locator('visible=true').first().click({ timeout: 3000 });
      } catch {
        if (!optional) missed.push(raw);
      }
      await page.waitForTimeout(300);
    }
    if (broken) await page.evaluate(() => (document.body.style.minWidth = '2000px'));
    const file = `${name}-${theme}-${width}.png`;
    await page.screenshot({ path: path.join(out, 'shots', file), animations: 'disabled' });
    const m = await page.evaluate(measure);
    rows.push({ file, status: res ? res.status() : 0, ...m, csp: await page.evaluate(() => window.__csp), errors: [...errors], missed });
  }
  await context.close();
}

function problem(r) {
  const found = [];
  if (r.status >= 400 || r.status === 0) found.push(`status ${r.status}`);
  if (r.overflow > 0) found.push(`horizontal overflow of ${r.overflow} px`);
  for (const o of r.outside) found.push(`${o.what} leaves the viewport: ${o.box}`);
  if (r.small.length) found.push(`${r.small.length} tap targets under 44 px, first ${JSON.stringify(r.small.slice(0, 3))}`);
  if (r.file.startsWith('log-') && !r.dividers) found.push('no day dividers');
  for (const z of r.zone) found.push(`day dividers and card times differ in zone: ${z}`);
  if (r.lowCount) found.push(`${r.lowCount} texts under contrast 4.5, first ${JSON.stringify(r.low.slice(0, 3))}`);
  for (const c of r.csp) found.push(`CSP violation: ${c}`);
  for (const e of r.errors) found.push(`console error: ${e}`);
  for (const c of r.missed) found.push(`nothing to click for ${c}`);
  return found;
}

fs.rmSync(path.join(out, 'shots'), { recursive: true, force: true });
fs.mkdirSync(path.join(out, 'shots'), { recursive: true });
const server = await serve();
const rows = [];
let browser;
try {
  browser = await playwright.chromium.launch();
  console.log(`chromium ${browser.version()} on ${process.platform}`);
  for (const width of widths) for (const theme of themes) await render(browser, server, width, theme, rows);
} finally {
  await browser?.close();
  await server.close();
}
fs.writeFileSync(path.join(out, 'run.json'), `${JSON.stringify(rows, null, 1)}\n`);
let failed = 0;
for (const r of rows) {
  const found = problem(r);
  for (const f of found) console.log(`${asserting ? 'FAIL' : 'note'} ${r.file}: ${f}`);
  failed += found.length ? 1 : 0;
}
console.log(`${rows.length} states, ${failed} with problems, images in ${path.join(out, 'shots')}`);
if (asserting && failed) process.exit(3);
