import fs from 'node:fs';
import path from 'node:path';
import { fixture } from './fixture.mjs';
import { Box, Client, candidate, check, commit, distDir, equal, exe, fails, lacks, luvusTested, matches, ok, phase, schema, serve, startBoard, stopAll, textOf, toon, version, win } from './lib.mjs';

const missing = (h) => `${h} not found on PATH`;
const profileFix = 'invalid: claude not found on PATH; install claude, or add its folder to PATH';

async function cli(box, srv) {
  const v = toon((await ok(await box.hand(['version']), 'hand version')).out).fields;
  equal([v.version, v.channel, v.commit, v.schema, v.luvus], [version, 'stable', commit(), String(schema), luvusTested], 'hand version reports the candidate build');

  const made = await fixture(box);
  const init = toon(made.init.out).fields;
  matches(init.id, /^[0-9a-f]{13}$/, 'hand init prints a fleet id');
  equal(init.created, 'true', 'hand init created the fleet');
  matches(init.luvus, new RegExp(`pinned`), 'hand init pinned a Luvus');
  matches(init.luvus, new RegExp(luvusTested.replace(/\./g, '\\.')), 'the pinned Luvus is the tested version');
  for (const f of ['hand.db', 'AGENTS.md', 'routing.json', path.join('memory', 'operator.md')]) check(fs.existsSync(path.join(box.fleet, f)), `hand init wrote ${f}`);
  const again = toon((await ok(await box.hand(['init']), 'a second hand init')).out).fields;
  equal([again.created, again.id], ['false', init.id], 'a second hand init finds the same fleet');

  const fleets = toon((await ok(await box.hand(['fleet', 'list']), 'hand fleet list')).out).tables.fleets;
  equal(fleets.map((f) => [f.id, f.name, f.state]), [[init.id, 'fleet', 'ok']], 'the fleet is registered and found');

  const projects = toon((await ok(await box.hand(['project', 'list']), 'hand project list')).out).tables.projects;
  equal(projects.map((p) => p.name ?? p.project), ['alpha', 'beta'], 'both projects are listed');
  const tasks = toon((await ok(await box.hand(['task', 'list', '--status', 'all']), 'hand task list')).out).tables.tasks;
  equal(tasks.map((t) => [t.id, t.title]).sort(), [['t1', 'Fix the login redirect'], ['t2', 'Add a CSV export'], ['t3', 'Update the README']], 'the three tasks are listed');

  const orient = await ok(await box.hand(['orient']), 'hand orient');
  check(Buffer.byteLength(orient.out) < 6000, `hand orient stays under its byte budget (${Buffer.byteLength(orient.out)} bytes)`);
  matches(orient.out, /^tasks: inbox=2 active=1 done=0 abandoned=0$/m, 'hand orient counts the tasks');
  matches(orient.out, /^fleet: fleet \(/m, 'hand orient names the fleet');

  const route = await ok(await box.hand(['route', 'list']), 'hand route list exits 0 with no harness installed');
  const profiles = toon(route.out).tables.profiles;
  equal(profiles.map((p) => p.name).sort(), ['deep', 'default', 'high', 'max', 'quick'], 'hand route list shows the five profiles');
  check(profiles.every((p) => p.valid === profileFix), 'every profile says why it is invalid, with a fix');

  const models = toon((await ok(await box.hand(['models']), 'hand models exits 0 with no harness installed')).out);
  equal(models.tables.listings.map((l) => [l.harness, l.state]), ['claude', 'codex', 'opencode', 'agy'].map((h) => [h, missing(h)]), 'hand models gives each harness a reason, not an error');
  check(models.tables.models.some((m) => m.harness === 'claude' && m.source === 'built-in'), 'hand models lists the built-in Claude models');
  check(!models.tables.models.some((m) => m.source === 'discovered'), 'hand models discovers no model without a harness');

  const doctor = await box.hand(['doctor']);
  await fails(doctor, 'hand doctor exits 1 because no profile has a harness', 1);
  const checks = toon(doctor.out).tables.checks;
  const state = (name) => checks.find((c) => c.check === name)?.state;
  for (const name of ['luvus pin', 'schema', 'fleet', 'routing.json', 'project alpha', 'project beta']) equal(state(name), 'ok', `hand doctor: ${name} is ok`);
  const git = checks.find((c) => c.check === 'git');
  equal([git.state, git.fix], win ? ['warn', 'core.longpaths is off; run `git config --global core.longpaths true`'] : ['ok', ''], `hand doctor: git is ${win ? 'a warning for core.longpaths on Windows' : 'ok'}`);
  const failing = checks.filter((c) => c.state === 'fail');
  equal(failing.map((c) => c.check).sort(), ['profile deep', 'profile default', 'profile high', 'profile max', 'profile quick'], 'hand doctor: only the five profiles fail');
  check(failing.every((c) => c.fix === profileFix), 'hand doctor: each failing profile names the missing claude');
  check(checks.every((c) => !/^harness /.test(c.check) || c.state !== 'fail'), 'hand doctor: no harness row fails');
  matches(doctor.err, /doctor: 5 check\(s\) failed/, 'hand doctor says how many checks failed');
  equal(toon(doctor.out).fields.server, 'not running', 'hand doctor: the Luvus server is not running yet');
  return made;
}

async function board(box, srv, made) {
  const link = await startBoard(box);
  const id = toon(made.init.out).fields.id;
  const origin = link.origin;
  equal(link.pathname, `/${id}/`, 'the login link points at the fleet page');
  matches(link.searchParams.get('token') ?? '', /^[0-9a-f]{48}$/, 'the login link carries the fleet token');
  const addr = box.read('board.addr').trim();
  equal(`http://${addr}`, origin, 'board.addr names the address the link uses');

  const laptop = new Client(origin);
  const anon = await laptop.get(`/${id}/`);
  equal(anon.status, 403, 'the fleet page refuses a request without the token');
  check(!anon.text.includes('Fix the login redirect'), 'the refusal shows nothing of the fleet');
  const wrong = await laptop.get(`/${id}/?token=${'0'.repeat(48)}`);
  equal(wrong.status, 403, 'a wrong token is refused');
  const login = await laptop.get(`${link.pathname}${link.search}`);
  equal([login.status, login.location], [303, `/${id}/`], 'the login link redirects to the fleet page');
  check(login.cookies.some((c) => c.startsWith(`hand_board_${id}=`) && /HttpOnly/i.test(c) && /SameSite=Strict/i.test(c)), 'the login sets an HttpOnly, SameSite=Strict cookie');

  const page = await laptop.get(`/${id}/`);
  equal(page.status, 200, 'the fleet page answers 200 with the cookie');
  for (const region of ['status', 'console', 'timeline', 'queue', 'tasks']) matches(page.text, new RegExp(`data-region="${region}"`), `the fleet page has the ${region} region`);
  matches(textOf(page.text), /Fix the login redirect/, 'the fleet page lists a task');
  matches(page.headers.get('content-security-policy') ?? '', /default-src 'none'; script-src 'self'; style-src 'self'/, 'the page carries the strict CSP');
  lacks(page.text, /rel="manifest"|data-worker/, 'a loopback page does not offer the install manifest');
  equal((await laptop.get('/')).status, 200, 'the fleet list answers on loopback');
  matches((await laptop.get('/')).text, new RegExp(`href="/${id}/"`), 'the fleet list links the fleet');

  const events = await laptop.get(`/${id}/events`, { stream: true, timeout: 40_000 });
  equal(events.status, 200, '/events answers 200');
  matches(events.headers.get('content-type') ?? '', /text\/event-stream/, '/events streams');
  const reader = events.res.body.getReader();
  let seen = '';
  const deadline = Date.now() + 30_000;
  while (Date.now() < deadline && !seen.includes('event: beat')) {
    const { value, done } = await reader.read();
    if (done) break;
    seen += Buffer.from(value).toString();
  }
  await reader.cancel();
  matches(seen, /event: status/, '/events sends the status first');
  matches(seen, /event: beat/, '/events sends a heartbeat when nothing else changed');
  equal((await new Client(origin).get(`/${id}/events`)).status, 403, '/events refuses a request without a session');

  const doc = await laptop.get(`/${id}/doc/docs/export-notes.md`);
  equal(doc.status, 200, 'the document viewer serves a file inside the fleet folder');
  matches(doc.text, /<h[1-6][^>]*>Export notes<\/h[1-6]>/, 'the viewer renders the Markdown');
  matches((await laptop.get(`/${id}/doc/docs/export-notes.md?raw`)).text, /^# Export notes/, 'the raw file is served too');
  matches(textOf((await laptop.get(`/${id}/doc/docs/nested/log.txt`)).text), /line one/, 'the viewer shows a text file');
  const token = fs.readFileSync(path.join(box.fleet, 'board.token'), 'utf8').trim();
  for (const refused of ['hand.db', 'board.token', 'AGENTS.md', 'luvus/note.txt', 'locks/note.txt', 'projects/note.txt', '.claude']) {
    const res = await laptop.get(`/${id}/doc/${refused}`);
    check(res.status === 403 && /does not show Hand&#39;s own files/.test(res.text) && !res.text.includes('SQLite format') && !res.text.includes(token) && !res.text.includes('inside '), `the viewer refuses ${refused} with the reason`);
  }
  const climbed = await laptop.raw(`/${id}/doc/docs/../hand.db`);
  check(climbed.status !== 200 && !climbed.text.includes('SQLite format'), `the viewer does not serve hand.db through a .. segment (${climbed.status})`);
  const encoded = await laptop.raw(`/${id}/doc/docs/%2e%2e/hand.db`);
  check(encoded.status !== 200 && !encoded.text.includes('SQLite format'), `the viewer does not serve hand.db through an encoded .. segment (${encoded.status})`);
  equal((await new Client(origin).get(`/${id}/doc/docs/export-notes.md`)).status, 403, 'the viewer refuses a request without a session');

  const funnel = await new Client(origin, { 'Tailscale-Funnel-Request': '?1' }).get(`/${id}/`);
  equal(funnel.status, 403, 'a Tailscale Funnel request is refused');
  matches(textOf(funnel.text), /does not answer the public internet/, 'the Funnel refusal says why');

  const connect = await laptop.get(`/${id}/connect`);
  equal(connect.status, 200, 'the laptop opens the Phone page');
  const csrf = connect.text.match(/name="csrf" value="([^"]+)"/)?.[1];
  check(Boolean(csrf), 'the Phone page carries a form token');
  const minted = await laptop.post(`/${id}/connect/code`, { csrf, base: origin });
  equal(minted.status, 200, 'the laptop mints a pairing code');
  const code = minted.text.match(/class="code-big"[^>]*>([A-Z0-9]{5}-[A-Z0-9]{5})</)?.[1];
  check(Boolean(code), `the page shows a pairing code (${code?.replace(/./g, '*')})`);
  matches(minted.text, new RegExp(`${origin.replace(/[.]/g, '\\.')}/${id}/pair#${code}`), 'the pairing link holds the code after the #');
  matches(minted.text, /<svg[^>]*QR code/, 'the page draws a QR code on its own');

  const proxy = { 'X-Forwarded-For': '100.64.0.2', 'X-Forwarded-Proto': 'https' };
  const phone = new Client(origin, proxy);
  const form = await phone.get(`/${id}/pair`);
  equal(form.status, 200, 'the phone opens the pairing form');
  matches(form.text, /name="code"/, 'the form asks for the code');
  equal((await phone.get(`/${id}/`)).status, 403, 'an unpaired phone is refused');
  const wrongCode = await phone.post(`/${id}/pair`, { code: 'AAAAA-AAAAA', name: 'E2E phone' });
  equal(wrongCode.status, 403, 'a wrong code is refused');
  const paired = await phone.post(`/${id}/pair`, { code: code.toLowerCase(), name: 'E2E phone' });
  equal([paired.status, paired.location], [303, `/${id}/`], 'the right code pairs the phone and redirects to the fleet');
  check(paired.cookies.some((c) => c.startsWith(`__Host-hand_phone_${id}=`) && /HttpOnly/i.test(c) && /SameSite=Strict/i.test(c) && /Secure/i.test(c)), 'the phone gets its own HttpOnly, Secure, SameSite=Strict cookie');
  check(!paired.cookies.some((c) => c.includes(fs.readFileSync(path.join(box.fleet, 'board.token'), 'utf8').trim())), 'the phone never receives the fleet token');
  const reuse = await new Client(origin, proxy).post(`/${id}/pair`, { code, name: 'second phone' });
  equal(reuse.status, 403, 'the code works once');
  matches(textOf(reuse.text), /already used/, 'a spent code says so');

  const home = await phone.get(`/${id}/`);
  equal(home.status, 200, 'the paired phone opens the fleet page');
  matches(home.text, /rel="manifest"/, 'behind the proxy headers the page offers the install manifest');
  matches(home.text, /data-worker/, 'behind the proxy headers the page registers its worker');
  const rail = await phone.get('/');
  equal(rail.status, 200, 'the paired phone opens the fleet list');
  matches(rail.text, new RegExp(`href="/${id}/"`), 'the rail lists the fleet the phone is paired with');
  equal(phone.cookie(`__Host-hand_phone_${id}`)?.length > 20, true, 'the phone keeps its secret in the cookie');
  const strange = await new Client(origin, proxy).get('/');
  equal(strange.status, 200, 'a proxied request without a session gets the fleet list page');
  check(!strange.text.includes(id), 'it names no fleet');
  const mine = await phone.get(`/${id}/connect`);
  matches(textOf(mine.text), /This phone/, 'the Phone page reads "This phone" on the phone');
  const nested = await phone.post(`/${id}/connect/code`, { csrf: mine.text.match(/name="csrf" value="([^"]+)"/)?.[1], base: origin });
  equal(nested.status, 403, 'a paired phone cannot mint a code');

  for (const [route, type] of [['/manifest.webmanifest', /application\/manifest\+json/], ['/sw.js', /javascript/], ['/offline', /text\/html/]]) {
    const res = await new Client(origin, proxy).get(route);
    check(res.status === 200 && type.test(res.headers.get('content-type') ?? ''), `${route} answers 200 with ${type.source}`);
  }
  const manifest = JSON.parse((await new Client(origin, proxy).get('/manifest.webmanifest')).text);
  equal([manifest.scope, manifest.display, manifest.icons.length], ['/', 'standalone', 3], 'the manifest scopes the whole origin and lists three icons');
  const worker = (await new Client(origin, proxy).get('/sw.js')).text;
  matches(worker, /^const BUILD = "[0-9a-f]{12}";/, 'the worker carries a build hash');

  const listed = await laptop.get(`/${id}/connect`);
  matches(textOf(listed.text), /E2E phone/, 'the laptop lists the paired phone');
  const phoneId = listed.text.match(/action="[^"]*\/connect\/revoke"><input type="hidden" name="csrf" value="[^"]*"><input type="hidden" name="id" value="(\d+)"/)?.[1];
  check(Boolean(phoneId), 'the laptop offers Revoke for the phone');
  const revoked = await laptop.post(`/${id}/connect/revoke`, { csrf: listed.text.match(/name="csrf" value="([^"]+)"/)[1], id: phoneId });
  equal(revoked.status, 303, 'the laptop revokes the phone');
  const gone = await phone.get(`/${id}/`);
  equal(gone.status, 403, 'the revoked phone is refused at once');
  matches(textOf(gone.text), /no longer paired/, 'the refusal says the phone is no longer paired');
  check(!gone.text.includes('Fix the login redirect'), 'the refusal shows nothing of the fleet');
  equal((await laptop.get(`/${id}/`)).status, 200, 'the laptop stays signed in after the revoke');

  const doctor = toon((await box.hand(['doctor'])).out).tables.checks;
  equal(doctor.find((c) => c.check === 'board')?.state, 'ok', 'hand doctor: the board answers');

  const pid = box.read('board.pid');
  matches(pid ?? '', new RegExp(`^\\d+ \\S+ ${path.join(box.bin, `hand${exe}`).replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}\\s*$`), 'board.pid names the installed hand');
  equal(toon((await ok(await box.hand(['watch', 'stop']), 'hand watch stop')).out).fields.watcher, 'not running', 'no watcher runs without a supervisor');
  equal(toon((await ok(await box.hand(['board', 'stop']), 'hand board stop')).out).fields.board, 'stopped', 'hand board stop stops the board');
  check(box.read('board.pid') === null, 'board.pid is gone');
  const down = await fetch(`${origin}/`, { signal: AbortSignal.timeout(3000) }).then(() => 'answers', () => 'refused');
  equal(down, 'refused', 'the board no longer answers');
  equal(toon((await ok(await box.hand(['board', 'stop']), 'a second hand board stop')).out).fields.board, 'not running', 'a second stop says not running');
}

async function update(box, srv) {
  const res = await ok(await box.hand(['update', '--check'], { env: { HAND_INSTALL_BASE: srv.base } }), 'hand update --check against the local release server');
  const doc = toon(res.out);
  equal(doc.fields.status, 'up to date', 'hand update --check says up to date');
  equal(doc.fields.from, `${version} stable ${commit()}`, 'the running build is the candidate');
  equal(doc.fields.to, `${version} stable ${commit()}`, 'the served build is the same commit');
  equal(doc.fields.luvus, `${luvusTested} kept`, 'the Luvus pin stays');
  check(srv.requests.some((r) => r.startsWith('/latest/download/')), 'hand update fetched from the stable channel');
}

await phase('first-run', async () => {
  await candidate();
  const srv = await serve(distDir);
  const box = new Box('first').prepare();
  try {
    const made = await cli(box, srv);
    await board(box, srv, made);
    await update(box, srv);
  } finally {
    await stopAll(box);
    await srv.close();
  }
});
