import { spawn } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { initialize } from './fixture.mjs';
import { Box, Client, cleaned, soft, stub, asset, candidate, check, commit, distDir, equal, exe, fails, install, luvusTested, matches, nextCommit, ok, phase, run, saveLuvus, schema, seedLuvus, serve, sha256, shells, startBoard, stopAll, sums, tmpRoot, toon, version, win } from './lib.mjs';

const previous = 'v0.11.0';
const previousSchema = 9;
const previousLuvus = '0.14.3';
const profiles = ['profile deep', 'profile default', 'profile high', 'profile max', 'profile quick'];
const escape = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

const userVersion = (db) => {
  const fd = fs.openSync(db, 'r');
  const head = Buffer.alloc(4);
  fs.readSync(fd, head, 0, 4, 60);
  fs.closeSync(fd);
  return head.readUInt32BE(0);
};

async function released() {
  const root = path.join(tmpRoot, 'previous');
  const dir = path.join(root, 'download', previous);
  if (!fs.existsSync(path.join(dir, asset))) {
    fs.mkdirSync(dir, { recursive: true });
    let got;
    for (let attempt = 1; attempt <= 3; attempt++) {
      got = await run('gh', ['release', 'download', previous, '--repo', 'atqamz/hand', '--clobber', '-p', asset, '-p', sums, '-D', dir], { env: process.env });
      if (got.code === 0) break;
      await new Promise((r) => setTimeout(r, attempt * 5000));
    }
    await ok(got, `gh release download ${previous}`);
  }
  for (const script of ['install.sh', 'install.ps1']) fs.copyFileSync(path.join(distDir, script), path.join(root, script));
  const line = fs.readFileSync(path.join(dir, sums), 'utf8').split(/\r?\n/).find((l) => l.trim().endsWith(asset));
  equal(line.trim().split(/\s+/)[0], sha256(path.join(dir, asset)), `the ${previous} archive matches its published checksum`);
  return root;
}

async function installOld(box, previousRoot) {
  const dir = path.join(box.dir, 'bin-old');
  const srv = await serve(previousRoot);
  try {
    await ok(await install(shells.at(-1), srv.base, dir, { HAND_INSTALL_VERSION: previous }, box), `the installer installs ${previous}`);
  } finally {
    await srv.close();
  }
  check(fs.existsSync(path.join(dir, `hand${exe}`)), `${previous} is installed`);
  box.bin = dir;
  const v = toon((await ok(await box.hand(['version']), 'hand version')).out).fields;
  equal([v.version, v.channel, v.schema], [previous.slice(1), 'stable', String(previousSchema)], `the installed build is ${previous}`);
  return { version: previous.slice(1), commit: v.commit, schema: previousSchema, sha: sha256(path.join(dir, `hand${exe}`)) };
}

async function installCandidate(box, srv) {
  const dir = path.join(box.dir, 'bin-candidate');
  await ok(await install(shells.at(-1), srv.base, dir, {}, box), 'the installer installs the candidate');
  check(fs.existsSync(path.join(dir, `hand${exe}`)), 'the candidate is installed');
  box.bin = dir;
  return { version, commit: commit(), schema, sha: sha256(path.join(dir, `hand${exe}`)) };
}

async function snapshot(box) {
  const attempts = toon((await ok(await box.hand(['attempt', 'list']), 'hand attempt list')).out).tables.attempts.map((a) => [a.id, a.task, a.harness, a.model, a.status]);
  const reports = [];
  for (const r of toon((await ok(await box.hand(['report', 'list']), 'hand report list')).out).tables.reports) {
    const shown = toon((await ok(await box.hand(['report', 'show', r.id]), `hand report show ${r.id}`)).out).fields;
    reports.push([r.id, r.attempt, r.task, r.status, shown.body]);
  }
  const list = toon((await ok(await box.hand(['task', 'list', '--status', 'inbox,active,done,abandoned']), 'hand task list')).out).tables.tasks;
  const tasks = {};
  for (const t of list) {
    const shown = toon((await ok(await box.hand(['task', 'show', t.id]), `hand task show ${t.id}`)).out);
    const f = shown.fields;
    const plan = f.plan === 'none' ? '' : toon((await ok(await box.hand(['plan', 'show', t.id]), `hand plan show ${t.id}`)).out).fields.body;
    tasks[t.id] = [f.project, f.title, f.status, f.goal, f.plan, plan, f.attempt, f.report, shown.tables.decisions.map((d) => [d.id, d.status, d.question])];
  }
  const decisions = [];
  for (const d of toon((await ok(await box.hand(['decision', 'list', '--all']), 'hand decision list')).out).tables.decisions) {
    const shown = toon((await ok(await box.hand(['decision', 'show', d.id]), `hand decision show ${d.id}`)).out).fields;
    decisions.push([d.id, d.task, d.status, d.question, shown.answer, shown.answered_by, shown.closed_at]);
  }
  const orient = toon((await ok(await box.hand(['orient']), 'hand orient')).out);
  return { tasks, decisions, attempts, reports, counts: orient.fields.tasks, cursor: Number(orient.fields.cursor) };
}

async function seed(box, label, withReport, pinned) {
  const alpha = await box.repo('alpha');
  const beta = await box.repo('beta');
  const seeded = seedLuvus(box, pinned);
  const init = toon((await initialize(box, `${label} hand init${seeded ? ' with the cached Luvus' : ' downloads the pinned Luvus'}`)).out).fields;
  saveLuvus(box);
  await ok(await box.hand(['project', 'add', 'alpha', alpha]), 'project add alpha');
  await ok(await box.hand(['project', 'add', 'beta', beta]), 'project add beta');
  await ok(await box.hand(['task', 'add', '--goal', 'People land on the home page after signing in.', 'alpha', 'Fix the login redirect']), 'task add t1');
  await ok(await box.hand(['task', 'add', '--goal', 'Let an operator download every task as CSV.', 'beta', 'Add a CSV export']), 'task add t2');
  await ok(await box.hand(['task', 'add', '--goal', 'Describe the install steps.', 'alpha', 'Update the README']), 'task add t3');
  await ok(await box.hand(['task', 'start', 't2']), 'task start t2');
  await ok(await box.hand(['plan', 'set', '--body', '1. Add the endpoint.\n2. Add the button.\n', 't2']), 'plan set t2');
  await ok(await box.hand(['decision', 'ask', '--file', '-', 't2'], { input: 'Which delimiter should the CSV export use?\n\n1. Comma — what most tools expect\n2. Semicolon — what spreadsheet locales in Europe expect\n' }), 'decision ask d1');
  await ok(await box.hand(['decision', 'answer', 'd1', '1. Comma — what most tools expect']), 'decision answer d1');
  await ok(await box.hand(['decision', 'ask', '--file', '-', 't2'], { input: 'Should the export include abandoned tasks?\n\n1. Yes\n2. No\n' }), 'decision ask d2');
  await ok(await box.hand(['task', 'start', 't3']), 'task start t3');
  await ok(await box.hand(['task', 'done', 't3']), 'task done t3');
  if (withReport) {
    const brief = path.join(box.dir, 'brief.md');
    fs.writeFileSync(brief, 'Change the redirect so that people land on the home page after signing in.\n');
    await ok(await box.hand(['task', 'start', 't1']), 'task start t1');
    const started = await ok(await box.hand(['attempt', 'start', '--harness', 'claude', '--model', 'sonnet', '--effort', 'low', '--prompt-file', brief, 't1'], { harness: stub(box), timeout: 180_000 }), 'attempt start with a stand-in harness');
    matches(started.out, /^attempt: a1$/m, 'the attempt is a1');
    await ok(await box.hand(['report', 'add', '--attempt', 'a1', '--status', 'done', '--text', `Changed the redirect.\n\nDelivered: ${path.join(box.fleet, 'AGENTS.md')}`]), 'report add a1');
    await ok(await box.hand(['attempt', 'stop', 'a1']), 'attempt stop a1');
  }
  return init.id;
}

async function watcher(box) {
  const bin = path.join(box.bin, `hand${exe}`);
  spawn(bin, ['watch', '--notify=false'], { cwd: box.fleet, env: box.env(), stdio: 'ignore', detached: true, windowsHide: true }).unref();
  for (let i = 0; i < 100 && !fs.existsSync(path.join(box.fleet, 'watch.pid')); i++) await new Promise((r) => setTimeout(r, 100));
  let text = '';
  for (let i = 0; i < 50 && !/^\d+ \S+ \S/.test(text); i++) {
    text = fs.readFileSync(path.join(box.fleet, 'watch.pid'), 'utf8');
    if (!/^\d+ \S+ \S/.test(text)) await new Promise((r) => setTimeout(r, 100));
  }
  check(/^\d+ \S+ \S/.test(text), 'a watcher runs and records watch.pid');
  return Number(text.split(' ')[0]);
}

async function update(box, s) {
  const { name, from, to, before, running } = s;
  const bin = path.join(box.bin, `hand${exe}`);
  const backups = path.join(box.root, 'backups');
  equal(userVersion(path.join(box.fleet, 'hand.db')), from.schema, `${name}: the fleet's database is at schema ${from.schema}`);

  const checked = toon((await ok(await box.hand(['update', '--check'], { cwd: box.dir, env: { HAND_INSTALL_BASE: to.srv.base } }), `${name}: hand update --check`)).out);
  equal(checked.fields.status, 'checked', `${name}: --check reports what would change`);
  check(!fs.existsSync(backups), `${name}: --check backed nothing up`);
  equal(sha256(bin), from.sha, `${name}: --check changed no binary`);
  check(!fs.existsSync(box.file('update.json')), `${name}: --check wrote no journal`);

  to.srv.requests.splice(0);
  const known = running.board && running.watcher && from.schema < schema;
  const res = await box.hand(['update'], { cwd: box.dir, env: { HAND_INSTALL_BASE: to.srv.base }, timeout: 300_000 });
  if (known) await fails(res, `${name}: hand update exits non-zero`, 1);
  else await ok(res, `${name}: hand update`);
  const doc = toon(res.out);
  equal(doc.fields.status, 'updated', `${name}: the update says updated`);
  equal(doc.fields.from, `${from.version} stable ${from.commit}`, `${name}: it started from the installed build`);
  equal(doc.fields.to, `${version} stable ${to.commit}`, `${name}: it moved to the served build`);
  equal(doc.fields.luvus, `${luvusTested} kept`, `${name}: the Luvus pin stays`);
  check(to.srv.requests.some((r) => r.startsWith('/latest/download/')), `${name}: it fetched the stable channel`);
  const made = doc.lists.backups ?? [];
  equal(made.length, 2, `${name}: it lists two backups`);
  const dbBackup = made.find((p) => path.basename(p).startsWith('hand.db.'));
  const binBackup = made.find((p) => /^hand\.\d/.test(path.basename(p)));
  check(Boolean(dbBackup) && fs.existsSync(dbBackup) && dbBackup.includes(path.join('backups', s.id)), `${name}: the hand.db backup exists under backups/<fleet id>`);
  check(Boolean(binBackup) && fs.existsSync(binBackup) && path.dirname(binBackup) === backups, `${name}: the old-binary backup exists under backups`);
  equal(userVersion(dbBackup), from.schema, `${name}: the hand.db backup keeps schema ${from.schema}`);
  equal(sha256(binBackup), from.sha, `${name}: the binary backup is the old binary`);
  equal(doc.tables.fleets.map((f) => [f.name, f.init]), [['fleet', 'ok']], `${name}: hand init ran in the fleet`);
  const units = doc.tables.units.map((u) => [u.name.split(' ')[0], u.action, u.result]);
  const wanted = [];
  if (running.watcher) wanted.push(['watch', 'stop', 'ok'], ['watch', 'start', 'ok']);
  if (running.board) wanted.push(['board', 'stop', 'ok'], ['board', 'start', 'ok']);
  if (known) {
    matches(units.at(-1)[2], new RegExp(`^failed: invalid: state schema ${schema} is newer than this hand \\(${from.schema}\\)$`), `${name}: the old updater cannot restart the Board once the watcher migrated the database`);
    equal(units.slice(0, -1), wanted.slice(0, -1), `${name}: every other step ran`);
    soft(false, `${name}: the Board did not restart: ${from.version}'s update opens hand.db after the new watcher migrated it; run hand open after the update`);
  } else {
    equal(units, wanted, `${name}: the stop and start steps are the ones that were running`);
    check(!doc.lists.help && !/failed/.test(res.out), `${name}: no step failed and no help line`);
  }
  check(!fs.existsSync(box.file('update.json')), `${name}: the journal is gone after a clean update`);
  if (win) check(fs.existsSync(`${bin}.old`), `${name}: Windows renamed the running executable to hand.exe.old`);

  const v = toon((await ok(await box.hand(['version']), `${name}: hand version after the update`)).out).fields;
  equal([v.version, v.channel, v.commit, v.schema], [version, 'stable', to.commit, String(schema)], `${name}: the installed binary is the served build`);
  if (win) {
    await cleaned(box.bin, () => box.hand(['version']));
    equal(fs.readdirSync(box.bin), ['hand.exe'], `${name}: the next hand run removed hand.exe.old`);
  }
  const after = await snapshot(box);
  equal(after.tasks, before.tasks, `${name}: every task, plan body, decision and answer is unchanged`);
  equal(after.decisions, before.decisions, `${name}: the decision list is unchanged`);
  equal(after.attempts, before.attempts, `${name}: every attempt is unchanged`);
  equal(after.reports, before.reports, `${name}: every report is unchanged`);
  equal(after.counts, before.counts, `${name}: the task counts are unchanged`);
  check(after.cursor >= before.cursor, `${name}: the event cursor did not go back (${before.cursor} to ${after.cursor})`);
  return { dbBackup, binBackup };
}

async function afterwards(box, s) {
  const { name, to, running, id } = s;
  const doctor = await box.hand(['doctor']);
  await ok(doctor, `${name}: hand doctor exits 0 with no harness installed`);
  const checks = toon(doctor.out).tables.checks;
  equal(checks.filter((c) => c.state === 'fail'), [], `${name}: hand doctor fails nothing`);
  equal(checks.filter((c) => c.state === 'warn' && profiles.includes(c.check)).map((c) => c.check).sort(), [...profiles].sort(), `${name}: hand doctor warns for the five profiles`);
  for (const c of ['schema', 'fleet', 'luvus pin']) equal(checks.find((r) => r.check === c)?.state, 'ok', `${name}: hand doctor: ${c} is ok`);
  const orient = await ok(await box.hand(['orient']), `${name}: hand orient`);
  check(Buffer.byteLength(orient.out) < 6000, `${name}: hand orient stays under its byte budget`);

  const bin = path.join(box.bin, `hand${exe}`);
  const link = await startBoard(box);
  const marker = box.read('board.pid') ?? '';
  matches(marker, new RegExp(`^\\d+ \\S+ ${escape(bin)}\\s*$`), `${name}: board.pid names the updated binary`);
  if (running.board) equal(Number(marker.split(' ')[0]) !== running.boardPid, true, `${name}: the Board is a new process`);
  const laptop = new Client(link.origin);
  await laptop.get(`${link.pathname}${link.search}`);
  const page = await laptop.get(`/${id}/`);
  equal(page.status, 200, `${name}: the Board serves the fleet from the updated binary`);
  matches(page.text, /Add a CSV export/, `${name}: the Board shows the migrated tasks`);
  if (running.watcher) {
    const now = fs.readFileSync(path.join(box.fleet, 'watch.pid'), 'utf8');
    matches(now, new RegExp(`^\\d+ \\S+ ${escape(bin)}\\s*$`), `${name}: watch.pid names the updated binary`);
    equal(Number(now.split(' ')[0]) !== running.watcherPid, true, `${name}: the watcher is a new process`);
  }

  to.srv.requests.splice(0);
  const again = toon((await ok(await box.hand(['update'], { cwd: box.dir, env: { HAND_INSTALL_BASE: to.srv.base } }), `${name}: a second hand update`)).out);
  equal(again.fields.status, 'up to date', `${name}: the second update says up to date`);
  equal(again.tables.units, [], `${name}: it touched neither the Board nor a watcher`);
  equal(box.read('board.pid'), marker, `${name}: the Board kept running through the second update`);
  equal(fs.readdirSync(path.join(box.root, 'backups')).filter((n) => /^hand\.\d/.test(n)).length, 1, `${name}: it made no new backup`);
}

async function restore(box, s, backup) {
  const { name, from, before } = s;
  const bin = path.join(box.bin, `hand${exe}`);
  equal(toon((await ok(await box.hand(['board', 'stop']), `${name}: hand board stop`)).out).fields.board, 'stopped', `${name}: the Board stops`);
  await ok(await box.hand(['watch', 'stop']), `${name}: hand watch stop`);

  if (from.schema < schema) {
    const refusing = path.join(box.dir, `hand-${from.version}${exe}`);
    fs.copyFileSync(backup.binBackup, refusing);
    fs.chmodSync(refusing, 0o755);
    const refused = await run(refusing, ['orient'], { cwd: box.fleet, env: box.env({}, [box.dir]) });
    await fails(refused, `${name}: the old binary refuses the migrated database`);
    matches(refused.out + refused.err, new RegExp(`newer than this hand \\(${from.schema}\\)`), `${name}: it says the schema is newer than this hand`);
    equal(userVersion(path.join(box.fleet, 'hand.db')), schema, `${name}: the refusal changed nothing, and the database is at schema ${schema}`);
  } else {
    equal(userVersion(path.join(box.fleet, 'hand.db')), schema, `${name}: the database is at schema ${schema}`);
  }

  for (const f of ['hand.db-wal', 'hand.db-shm']) fs.rmSync(path.join(box.fleet, f), { force: true });
  fs.copyFileSync(backup.dbBackup, path.join(box.fleet, 'hand.db'));
  fs.copyFileSync(backup.binBackup, bin);
  fs.chmodSync(bin, 0o755);
  const v = toon((await ok(await box.hand(['version']), `${name}: hand version after the restore`)).out).fields;
  equal([v.version, v.schema], [from.version, String(from.schema)], `${name}: the restored binary is the previous one`);
  equal(userVersion(path.join(box.fleet, 'hand.db')), from.schema, `${name}: the restored database is at schema ${from.schema}`);
  const restored = await snapshot(box);
  equal(restored.tasks, before.tasks, `${name}: the restored fleet has every task, plan and decision`);
  equal(restored.attempts, before.attempts, `${name}: the restored fleet has every attempt`);
  equal(restored.reports, before.reports, `${name}: the restored fleet has every report`);
  equal(restored.counts, before.counts, `${name}: the restored task counts match`);
  equal(restored.cursor, before.cursor, `${name}: the restored event cursor matches`);

  const link = await startBoard(box);
  const laptop = new Client(link.origin);
  await laptop.get(`${link.pathname}${link.search}`);
  equal((await laptop.get(`/${s.id}/`)).status, 200, `${name}: the previous Board serves the restored fleet`);
  const pid = Number(box.read('board.pid').split(' ')[0]);
  process.kill(pid);
  for (let i = 0; i < 100 && box.read('board.pid') !== null; i++) {
    try {
      process.kill(pid, 0);
    } catch {
      break;
    }
    await new Promise((r) => setTimeout(r, 100));
  }
  fs.rmSync(box.file('board.pid'), { force: true });
  fs.rmSync(box.file('board.addr'), { force: true });
}

async function scenario(s) {
  const { box, name, running } = s;
  const before = await snapshot(box);
  check(Object.keys(before.tasks).length === 3 && before.decisions.length === 2, `${name}: the fleet holds three tasks and two decisions`);
  equal([before.attempts.length, before.reports.length], s.report ? [1, 1] : [0, 0], `${name}: the fleet holds ${s.report ? 'an attempt and a report' : 'no attempt and no report'}`);
  if (running.board) {
    await startBoard(box);
    running.boardPid = Number(box.read('board.pid').split(' ')[0]);
    check(Boolean(running.boardPid), `${name}: a Board runs on the old binary`);
  } else {
    equal(box.read('board.pid'), null, `${name}: no Board runs`);
  }
  if (running.watcher) running.watcherPid = await watcher(box);
  else check(!fs.existsSync(path.join(box.fleet, 'watch.pid')), `${name}: no watcher runs`);
  s.before = before;
  const backup = await update(box, s);
  await afterwards(box, s);
  await restore(box, s, backup);
}

await phase('upgrade', async () => {
  await candidate();
  const previousRoot = await released();
  const current = await serve(distDir);
  const next = await serve(path.join(distDir, 'next'));
  let box;
  try {
    for (const [slug, running, report] of [['stopped', {}, true], ['board running', { board: true }, false], ['board and watcher running', { board: true, watcher: true }, false]]) {
      box = new Box(`upgrade-${slug.replace(/ /g, '-')}`).prepare();
      const from = await installOld(box, previousRoot);
      const id = await seed(box, previous, report, previousLuvus);
      await scenario({ box, id, report, name: `${previous} to the candidate, ${slug}`, from, to: { srv: current, commit: commit() }, running });
      await stopAll(box);
    }
    box = new Box('upgrade-candidate').prepare();
    const from = await installCandidate(box, current);
    const id = await seed(box, 'the candidate', true, luvusTested);
    await scenario({ box, id, report: true, name: 'the candidate to the next build', from, to: { srv: next, commit: nextCommit() }, running: { board: true, watcher: true } });
    await stopAll(box);
  } finally {
    await current.close();
    await next.close();
    if (box) await stopAll(box);
  }
});
