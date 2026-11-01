import fs from 'node:fs';
import { createRequire } from 'node:module';
import path from 'node:path';

const [before, after, out] = process.argv.slice(2);
if (!before || !after || !out) {
  console.error('usage: node diff.mjs BEFORE_DIR AFTER_DIR OUT_DIR');
  process.exit(2);
}
const channel = 16;
const share = 0.001;
const playwright = createRequire(import.meta.url)(process.env.RENDER_PLAYWRIGHT ?? 'playwright');

const names = fs.readdirSync(after).filter((f) => f.endsWith('.png')).sort();
fs.mkdirSync(out, { recursive: true });
const browser = await playwright.chromium.launch();
const page = await browser.newPage();
await page.setContent('<canvas></canvas>');
const rows = [];
for (const name of names) {
  const b = path.join(before, name);
  if (!fs.existsSync(b)) {
    rows.push({ name, note: 'new state' });
    continue;
  }
  const r = await page.evaluate(
    async ({ x, y, channel }) => {
      const load = (src) =>
        new Promise((resolve, reject) => {
          const img = new Image();
          img.onload = () => resolve(img);
          img.onerror = reject;
          img.src = src;
        });
      const [p, q] = await Promise.all([load(x), load(y)]);
      if (p.width !== q.width || p.height !== q.height) return { size: `${p.width}x${p.height} to ${q.width}x${q.height}` };
      const c = document.querySelector('canvas');
      c.width = p.width;
      c.height = p.height;
      const g = c.getContext('2d', { willReadFrequently: true });
      g.drawImage(p, 0, 0);
      const a = g.getImageData(0, 0, c.width, c.height);
      g.drawImage(q, 0, 0);
      const d = g.getImageData(0, 0, c.width, c.height);
      let changed = 0;
      for (let i = 0; i < a.data.length; i += 4) {
        const hit = Math.abs(a.data[i] - d.data[i]) > channel || Math.abs(a.data[i + 1] - d.data[i + 1]) > channel || Math.abs(a.data[i + 2] - d.data[i + 2]) > channel;
        if (hit) {
          changed++;
          d.data[i] = 255;
          d.data[i + 1] = 0;
          d.data[i + 2] = 0;
          d.data[i + 3] = 255;
        }
      }
      g.putImageData(d, 0, 0);
      return { changed, total: c.width * c.height, image: changed ? c.toDataURL('image/png') : '' };
    },
    { x: `data:image/png;base64,${fs.readFileSync(b).toString('base64')}`, y: `data:image/png;base64,${fs.readFileSync(path.join(after, name)).toString('base64')}`, channel },
  );
  if (r.size) rows.push({ name, note: `size changed, ${r.size}` });
  else if (r.changed / r.total > share) {
    fs.writeFileSync(path.join(out, `diff-${name}`), Buffer.from(r.image.split(',')[1], 'base64'));
    rows.push({ name, note: `${r.changed} px changed (${((100 * r.changed) / r.total).toFixed(1)}%)` });
  }
}
await browser.close();

const lines = [`### Board render diff`, '', rows.length ? `${rows.length} of ${names.length} states changed by more than ${share * 100}% of their pixels (a channel moved by more than ${channel}). Not a gate: fonts and the browser build can move pixels.` : `No state changed by more than ${share * 100}% of its pixels.`, ''];
for (const r of rows) lines.push(`- \`${r.name}\`: ${r.note}`);
const text = `${lines.join('\n')}\n`;
fs.writeFileSync(path.join(out, 'diff.md'), text);
console.log(text);
if (process.env.GITHUB_STEP_SUMMARY) fs.appendFileSync(process.env.GITHUB_STEP_SUMMARY, text);
