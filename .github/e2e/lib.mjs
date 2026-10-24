import { spawn } from 'node:child_process';
import crypto from 'node:crypto';
import fs from 'node:fs';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

export const win = process.platform === 'win32';
export const exe = win ? '.exe' : '';
export const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
export const tmpRoot = path.resolve(process.env.E2E_TMP ?? path.join(process.env.RUNNER_TEMP ?? os.tmpdir(), 'hand-e2e'));
export const distDir = path.resolve(process.env.E2E_DIST ?? path.join(tmpRoot, 'dist'));
export const logDir = path.join(tmpRoot, 'logs');
export const binDir = path.join(tmpRoot, 'bin');

const source = (rel) => fs.readFileSync(path.join(repoRoot, rel), 'utf8');
export const schema = Number(source('internal/state/state.go').match(/^const SchemaVersion = (\d+)$/m)[1]);
export const luvusTested = source('internal/luvus/pin.go').match(/^const Tested = "(.+)"$/m)[1];
export const version = '0.0.0-e2e';
export const goos = win ? 'windows' : process.platform;
export const goarch = process.arch === 'arm64' ? 'arm64' : 'amd64';
export const asset = win ? 'hand-windows-amd64.zip' : `hand-${goos}-${goarch}.tar.gz`;
export const sums = win ? 'hand-windows-amd64.sha256' : 'checksums.txt';
export const commit = () => fs.readFileSync(path.join(distDir, 'commit.txt'), 'utf8').trim().slice(0, 12);
export const nextCommit = () => fs.readFileSync(path.join(distDir, 'next', 'commit.txt'), 'utf8').trim().slice(0, 12);

const boxes = [];
let transcript = null;

export function logTo(name) {
  fs.mkdirSync(logDir, { recursive: true });
  transcript = path.join(logDir, `${name}.log`);
  fs.writeFileSync(transcript, '');
}

function note(text) {
  console.log(text);
  if (transcript) fs.appendFileSync(transcript, text + '\n');
}

export const pass = (msg) => note(`ok - ${msg}`);

export function check(ok, msg) {
  if (!ok) throw new Error(msg);
  pass(msg);
}

export function soft(ok, msg) {
  if (ok) pass(msg);
  else note(`::warning title=known issue::${msg}`);
}

export function equal(got, want, msg) {
  if (JSON.stringify(got) !== JSON.stringify(want)) {
    throw new Error(`${msg}: got ${JSON.stringify(got)}, want ${JSON.stringify(want)}`);
  }
  pass(msg);
}

export function matches(text, re, msg) {
  if (!re.test(text)) throw new Error(`${msg}: ${re} not found in:\n${text.slice(0, 1200)}`);
  pass(msg);
}

export function lacks(text, re, msg) {
  if (re.test(text)) throw new Error(`${msg}: ${re} found in:\n${text.slice(0, 1200)}`);
  pass(msg);
}

export function run(cmd, args = [], o = {}) {
  const shown = [cmd, ...args].map((a) => (/[\s"'$]/.test(a) ? JSON.stringify(a) : a)).join(' ');
  return new Promise((resolve, reject) => {
    const child = spawn(cmd, args, { cwd: o.cwd, env: o.env, stdio: ['pipe', 'pipe', 'pipe'], windowsHide: true });
    let out = '';
    let err = '';
    let done = false;
    const finish = (code, signal) => {
      if (done) return;
      done = true;
      clearTimeout(timer);
      const res = { code, signal, out, err, shown };
      note(`::group::$ ${shown}`);
      note(`exit ${code}${signal ? ` (${signal})` : ''}`);
      if (out.trim()) note(out.trimEnd());
      if (err.trim()) note(`stderr:\n${err.trimEnd()}`);
      note('::endgroup::');
      resolve(res);
    };
    const timer = setTimeout(() => {
      child.kill('SIGKILL');
      finish(-1, 'timeout');
    }, o.timeout ?? 180_000);
    child.stdout.on('data', (d) => (out += d));
    child.stderr.on('data', (d) => (err += d));
    child.on('error', (e) => {
      if (done) return;
      done = true;
      clearTimeout(timer);
      reject(e);
    });
    child.on('exit', (code, signal) => setTimeout(() => finish(code, signal), 3000).unref());
    child.on('close', (code, signal) => finish(code, signal));
    child.stdin.on('error', () => {});
    child.stdin.end(o.input ?? '');
  });
}

export async function ok(res, msg) {
  if (res.code !== 0) throw new Error(`${msg}: ${res.shown} exited ${res.code}\n${res.out}\n${res.err}`);
  pass(msg);
  return res;
}

export async function fails(res, msg, code = null) {
  if (res.code === 0 || (code !== null && res.code !== code)) {
    throw new Error(`${msg}: ${res.shown} exited ${res.code}, want ${code ?? 'non-zero'}\n${res.out}\n${res.err}`);
  }
  pass(msg);
  return res;
}

export function toon(text) {
  const doc = { fields: {}, tables: {}, lists: {} };
  let table = null;
  let list = null;
  const cell = (s) => (s.startsWith('"') ? JSON.parse(s) : s);
  const split = (line) => {
    const cells = [];
    let cur = '';
    let quoted = false;
    for (let i = 0; i < line.length; i++) {
      const c = line[i];
      if (c === '\\' && quoted) cur += c + line[++i];
      else if (c === '"') {
        quoted = !quoted;
        cur += c;
      } else if (c === ',' && !quoted) {
        cells.push(cell(cur));
        cur = '';
      } else cur += c;
    }
    cells.push(cell(cur));
    return cells;
  };
  for (const line of text.split(/\r?\n/)) {
    let m;
    if ((m = line.match(/^(\S[^[]*)\[(\d+)\]\{(.*)\}:$/))) {
      table = { cols: m[3].split(','), rows: [] };
      doc.tables[m[1]] = table.rows;
      list = null;
    } else if ((m = line.match(/^(\S[^[]*)\[(\d+)\]:$/))) {
      list = [];
      doc.lists[m[1]] = list;
      table = null;
    } else if (/^ {2}- /.test(line) && list) {
      list.push(line.slice(4));
    } else if (/^ {2}\S/.test(line) && table) {
      const cells = split(line.slice(2));
      table.rows.push(Object.fromEntries(table.cols.map((c, i) => [c, cells[i]])));
    } else if ((m = line.match(/^([A-Za-z0-9_]+): (.*)$/))) {
      doc.fields[m[1]] = cell(m[2]);
      table = list = null;
    }
  }
  return doc;
}

const harnesses = ['claude', 'codex', 'opencode', 'agy'];

function found(name, dirs) {
  const exts = win ? ['', '.exe', '.cmd', '.bat', '.com', '.ps1'] : [''];
  return dirs.flatMap((d) => exts.map((e) => path.join(d, name + e))).filter((p) => fs.existsSync(p));
}

function pathKey(env) {
  return Object.keys(env).find((k) => k.toLowerCase() === 'path') ?? 'PATH';
}

export function cleanEnv() {
  const env = {};
  for (const [k, v] of Object.entries(process.env)) {
    if (/^(PSModulePath$|LUVUS_|HAND_|SECONDHAND_|CLAUDE|ANTHROPIC|OPENAI|CODEX|GH_|GITHUB_|ACTIONS_|RUNNER_|XDG_RUNTIME_DIR$|DBUS_SESSION_BUS_ADDRESS$|NODE_OPTIONS$)/i.test(k)) continue;
    env[k] = v;
  }
  return env;
}

export class Box {
  constructor(name) {
    this.name = name;
    this.dir = path.join(tmpRoot, name);
    this.home = path.join(this.dir, 'home');
    this.root = path.join(this.dir, 'sh');
    this.fleet = path.join(this.dir, 'fleet');
    this.tmp = path.join(this.dir, 'tmp');
    this.bin = binDir;
    boxes.push(this);
  }

  prepare() {
    fs.rmSync(this.dir, { recursive: true, force: true });
    for (const d of [this.home, this.tmp, this.fleet]) fs.mkdirSync(d, { recursive: true });
    if (win) for (const d of ['AppData\\Local', 'AppData\\Roaming']) fs.mkdirSync(path.join(this.home, d), { recursive: true });
    return this;
  }

  env(extra = {}, dirs = [this.bin], harness = null) {
    const env = cleanEnv();
    const key = pathKey(env);
    const own = [...dirs, ...(process.env.E2E_PATH ?? env[key] ?? '').split(path.delimiter)].filter(Boolean);
    const stray = harnesses.flatMap((h) => found(h, own));
    if (harness) own.unshift(harness);
    else if (stray.length) throw new Error(`a harness is on PATH, so the no-harness checks cannot run: ${stray.join(', ')}`);
    env[key] = own.join(path.delimiter);
    Object.assign(env, { HOME: this.home, SECONDHAND_HOME: this.root, TMPDIR: this.tmp, TMP: this.tmp, TEMP: this.tmp, LANG: 'C.UTF-8', GIT_TERMINAL_PROMPT: '0', GIT_CONFIG_NOSYSTEM: '1' });
    if (win) Object.assign(env, { USERPROFILE: this.home, LOCALAPPDATA: path.join(this.home, 'AppData', 'Local'), APPDATA: path.join(this.home, 'AppData', 'Roaming') });
    return Object.assign(env, extra);
  }

  hand(args, o = {}) {
    const bin = o.bin ?? this.bin;
    return run(path.join(bin, `hand${exe}`), args, { cwd: o.cwd ?? this.fleet, env: this.env(o.env ?? {}, [bin], o.harness), input: o.input, timeout: o.timeout });
  }

  git(args, cwd) {
    return run('git', ['-c', 'user.name=e2e', '-c', 'user.email=e2e@example.invalid', '-c', 'commit.gpgsign=false', ...args], { cwd, env: this.env() });
  }

  async repo(name, files = { 'README.md': `# ${name}\n` }) {
    const dir = path.join(this.dir, `repo-${name}`);
    fs.mkdirSync(dir, { recursive: true });
    for (const [f, text] of Object.entries(files)) fs.writeFileSync(path.join(dir, f), text);
    await ok(await this.git(['init', '-q', '-b', 'main'], dir), `git init ${name}`);
    await ok(await this.git(['add', '.'], dir), `git add ${name}`);
    await ok(await this.git(['commit', '-q', '-m', 'initial'], dir), `git commit ${name}`);
    return dir;
  }

  file(...parts) {
    return path.join(this.root, ...parts);
  }

  read(...parts) {
    try {
      return fs.readFileSync(this.file(...parts), 'utf8');
    } catch {
      return null;
    }
  }
}

export async function diagnostics() {
  for (const b of boxes) {
    note(`::group::diagnostics ${b.name}`);
    for (const rel of ['board.log', 'update.log', 'update.json', 'board.addr', 'board.pid']) {
      const text = b.read(rel);
      if (text !== null) note(`--- ${rel}\n${text.slice(-6000)}`);
    }
    for (const name of ['watch.log']) {
      const p = path.join(b.fleet, name);
      if (fs.existsSync(p)) note(`--- ${name}\n${fs.readFileSync(p, 'utf8').slice(-4000)}`);
    }
    note('::endgroup::');
    if (fs.existsSync(path.join(b.bin, `hand${exe}`)) && fs.existsSync(b.fleet)) {
      await b.hand(['doctor']).catch(() => {});
    }
  }
}

const registry = "[Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $true)";

export async function userPath(shell = 'powershell') {
  const script = `$k = ${registry}; $k.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames); $k.Close()`;
  const res = await run(shell, ['-NoProfile', '-NonInteractive', '-Command', script], { env: cleanEnv() });
  if (res.code !== 0) throw new Error(`cannot read the user PATH: ${res.err}`);
  return res.out.trim();
}

export async function setUserPath(shell, value) {
  const script = `$k = ${registry}; $k.SetValue('Path', $env:E2E_PATH, [Microsoft.Win32.RegistryValueKind]::ExpandString); $k.Close()`;
  const res = await run(shell, ['-NoProfile', '-NonInteractive', '-Command', script], { env: { ...cleanEnv(), E2E_PATH: value } });
  if (res.code !== 0) throw new Error(`cannot restore the user PATH: ${res.err}`);
}

export async function phase(name, fn) {
  logTo(name);
  const before = win ? await userPath() : null;
  try {
    await fn();
    note(`PASS ${name}`);
  } catch (e) {
    console.log(`::error title=e2e ${name}::${String(e.message).split('\n')[0]}`);
    note(`FAIL ${name}: ${e.stack ?? e}`);
    await diagnostics();
    process.exitCode = 1;
  } finally {
    if (win) await setUserPath('powershell', before).catch((e) => note(`::warning::${e.message}`));
  }
  process.exit(process.exitCode ?? 0);
}

const types = { '.sh': 'text/plain; charset=utf-8', '.ps1': 'text/plain; charset=utf-8', '.txt': 'text/plain; charset=utf-8', '.sha256': 'text/plain; charset=utf-8', '.gz': 'application/gzip', '.zip': 'application/zip' };

export function serve(root) {
  const requests = [];
  const server = http.createServer((req, res) => {
    const rel = decodeURIComponent(new URL(req.url, 'http://x').pathname);
    const file = path.join(root, rel);
    requests.push(rel);
    if (!file.startsWith(root + path.sep) || !fs.existsSync(file) || fs.statSync(file).isDirectory()) {
      res.writeHead(404);
      res.end('not found');
      return;
    }
    res.writeHead(200, { 'content-type': types[path.extname(file)] ?? 'application/octet-stream', 'content-length': fs.statSync(file).size });
    fs.createReadStream(file).pipe(res);
  });
  return new Promise((resolve) =>
    server.listen(0, '127.0.0.1', () =>
      resolve({
        base: `http://127.0.0.1:${server.address().port}`,
        requests,
        close: () =>
          new Promise((done) => {
            server.closeAllConnections();
            server.close(done);
          }),
      }),
    ),
  );
}

export const shells = win ? ['powershell', 'pwsh'] : ['sh'];

export function install(shell, base, dir, extra = {}, box = null, dirs = []) {
  const env = box && !win ? box.env({}, dirs) : cleanEnv();
  Object.assign(env, { HAND_INSTALL_BASE: base, HAND_INSTALL_DIR: dir }, extra);
  if (shell === 'sh') return run('sh', ['-c', 'curl -fsSL "$HAND_INSTALL_BASE/install.sh" | sh'], { env });
  return run(shell, ['-NoProfile', '-NonInteractive', '-Command', 'irm "$env:HAND_INSTALL_BASE/install.ps1" | iex'], { env });
}

export async function candidate() {
  if (fs.existsSync(path.join(binDir, `hand${exe}`))) return binDir;
  const srv = await serve(distDir);
  try {
    const box = new Box('installer').prepare();
    await ok(await install(shells.at(-1), srv.base, binDir, {}, box), 'the installer installs the candidate');
    check(fs.existsSync(path.join(binDir, `hand${exe}`)), 'the candidate is installed');
  } finally {
    await srv.close();
  }
  return binDir;
}

export class Client {
  constructor(base, headers = {}) {
    this.base = base;
    this.headers = headers;
    this.jar = new Map();
  }

  cookie(name) {
    return this.jar.get(name);
  }

  async req(method, p, o = {}) {
    const headers = { ...this.headers, ...(o.headers ?? {}) };
    if (this.jar.size) headers.cookie = [...this.jar].map(([k, v]) => `${k}=${v}`).join('; ');
    const res = await fetch(new URL(p, this.base), { method, redirect: 'manual', headers, body: o.form ? new URLSearchParams(o.form) : undefined, signal: AbortSignal.timeout(o.timeout ?? 20_000) }).catch((e) => {
      throw new Error(`${method} ${this.base}${p}: ${e.message}: ${e.cause?.message ?? e.cause ?? ''}`);
    });
    for (const c of res.headers.getSetCookie()) {
      const [pair, ...attrs] = c.split(';');
      const i = pair.indexOf('=');
      const name = pair.slice(0, i);
      if (attrs.some((a) => /^\s*max-age=0\s*$/i.test(a))) this.jar.delete(name);
      else this.jar.set(name, pair.slice(i + 1));
    }
    const text = o.stream ? '' : await res.text();
    return { status: res.status, headers: res.headers, text, location: res.headers.get('location'), cookies: res.headers.getSetCookie(), res };
  }

  get(p, o) {
    return this.req('GET', p, o);
  }

  raw(rawPath) {
    const url = new URL(this.base);
    const headers = { ...this.headers };
    if (this.jar.size) headers.cookie = [...this.jar].map(([k, v]) => `${k}=${v}`).join('; ');
    return new Promise((resolve, reject) => {
      http
        .get({ host: url.hostname, port: url.port, path: rawPath, headers, timeout: 20_000 }, (res) => {
          let text = '';
          res.on('data', (d) => (text += d));
          res.on('end', () => resolve({ status: res.statusCode, text, location: res.headers.location ?? null }));
        })
        .on('error', reject);
    });
  }

  post(p, form, o = {}) {
    return this.req('POST', p, { ...o, form });
  }
}

export const textOf = (html) => html.replace(/<script[\s\S]*?<\/script>/g, '').replace(/<[^>]*>/g, ' ').replace(/&#39;/g, "'").replace(/&amp;/g, '&').replace(/\s+/g, ' ');

export async function startBoard(box) {
  const addr = process.env.E2E_BOARD_ADDR;
  if (addr) {
    const out = fs.openSync(path.join(box.dir, 'board.out'), 'a');
    const bin = path.join(box.bin, `hand${exe}`);
    spawn(bin, ['board', '--addr', addr], { cwd: box.fleet, env: box.env(), stdio: ['ignore', out, out], detached: true }).unref();
    for (let i = 0; i < 50 && !box.read('board.addr'); i++) await new Promise((r) => setTimeout(r, 100));
  }
  const open = await ok(await box.hand(['open', '--print']), 'hand open --print starts the Board and prints the login link');
  const url = toon(open.out).fields.url;
  check(Boolean(url), 'the login link is printed');
  return new URL(url);
}

export async function stopAll(box) {
  await box.hand(['board', 'stop']);
  await box.hand(['watch', 'stop']);
}

export function stub(box) {
  const dir = path.join(box.dir, 'harness');
  fs.mkdirSync(dir, { recursive: true });
  fs.copyFileSync(path.join(distDir, 'stub', `claude-${goos}-${goarch}${exe}`), path.join(dir, `claude${exe}`));
  fs.chmodSync(path.join(dir, `claude${exe}`), 0o755);
  return dir;
}

export const luvusCache = path.join(tmpRoot, 'luvus-cache');

export function seedLuvus(box, wanted) {
  if (process.env.E2E_FRESH_LUVUS === 'true' || !fs.existsSync(path.join(luvusCache, 'pin.json'))) return false;
  if (JSON.parse(fs.readFileSync(path.join(luvusCache, 'pin.json'), 'utf8')).version !== wanted) return false;
  const dest = path.join(box.root, 'luvus');
  fs.mkdirSync(box.root, { recursive: true });
  fs.cpSync(luvusCache, dest, { recursive: true });
  const pinFile = path.join(dest, 'pin.json');
  const pin = JSON.parse(fs.readFileSync(pinFile, 'utf8'));
  pin.path = path.join(dest, path.basename(path.dirname(pin.path)), path.basename(pin.path));
  fs.writeFileSync(pinFile, JSON.stringify(pin, null, 2));
  return true;
}

export function saveLuvus(box) {
  if (fs.existsSync(path.join(luvusCache, 'pin.json')) || !fs.existsSync(path.join(box.root, 'luvus', 'pin.json'))) return;
  fs.cpSync(path.join(box.root, 'luvus'), luvusCache, { recursive: true });
}

export async function cleaned(dir, probe) {
  for (let i = 0; i < 6 && fs.readdirSync(dir).some((n) => n.includes('.old')); i++) {
    await probe();
    await new Promise((r) => setTimeout(r, 2000));
  }
}

export function sha256(file) {
  return crypto.createHash('sha256').update(fs.readFileSync(file)).digest('hex');
}

