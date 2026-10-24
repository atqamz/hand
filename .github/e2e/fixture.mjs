import fs from 'node:fs';
import path from 'node:path';
import { luvusTested, ok, saveLuvus, seedLuvus } from './lib.mjs';

export async function initialize(box, msg) {
  let res;
  for (let attempt = 1; attempt <= 3; attempt++) {
    res = await box.hand(['init']);
    if (res.code === 0 || !/update: |download|timed out|connection|EOF|reset/i.test(res.err)) break;
    await new Promise((r) => setTimeout(r, attempt * 5000));
  }
  return ok(res, msg);
}

export async function fixture(box) {
  const out = { repos: {} };
  out.repos.alpha = await box.repo('alpha');
  out.repos.beta = await box.repo('beta');
  const seeded = seedLuvus(box, luvusTested);
  out.init = await initialize(box, `hand init${seeded ? ' with the cached Luvus' : ' downloads the pinned Luvus'}`);
  saveLuvus(box);
  const docs = path.join(box.fleet, 'docs');
  fs.mkdirSync(path.join(docs, 'nested'), { recursive: true });
  fs.writeFileSync(path.join(docs, 'export-notes.md'), '# Export notes\n\nThe export writes **one row per task**.\n\n| column | meaning |\n|---|---|\n| id | task ref |\n| title | task title |\n');
  fs.writeFileSync(path.join(docs, 'nested', 'log.txt'), 'line one\nline two\n');
  for (const hidden of ['luvus', 'locks', 'projects']) {
    fs.mkdirSync(path.join(box.fleet, hidden), { recursive: true });
    fs.writeFileSync(path.join(box.fleet, hidden, 'note.txt'), `inside ${hidden}\n`);
  }
  await ok(await box.hand(['project', 'add', 'alpha', out.repos.alpha]), 'hand project add alpha');
  await ok(await box.hand(['project', 'add', 'beta', out.repos.beta]), 'hand project add beta');
  await ok(await box.hand(['task', 'add', '--goal', 'People land on the home page after signing in.', 'alpha', 'Fix the login redirect']), 'hand task add t1');
  await ok(await box.hand(['task', 'add', '--goal', 'Let an operator download every task as CSV.', 'beta', 'Add a CSV export']), 'hand task add t2');
  await ok(await box.hand(['task', 'add', '--goal', 'Describe the install steps.', 'alpha', 'Update the README']), 'hand task add t3');
  await ok(await box.hand(['task', 'start', 't2']), 'hand task start t2');
  await ok(await box.hand(['plan', 'set', '--body', '1. Add the endpoint.\n2. Add the button.\n', 't2']), 'hand plan set t2');
  await ok(await box.hand(['decision', 'ask', '--file', '-', 't2'], { input: 'Which delimiter should the CSV export use?\n\n1. Comma — what most tools expect\n2. Semicolon — what spreadsheet locales in Europe expect\n3. Other\n' }), 'hand decision ask t2');
  await ok(await box.hand(['task', 'deliver', 't2', path.join(docs, 'export-notes.md')]), 'hand task deliver t2');
  return out;
}
