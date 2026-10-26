import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { Box, cleaned, cleanEnv, setUserPath, userPath, asset, candidate, check, commit, distDir, equal, exe, fails, install, luvusTested, matches, ok, phase, repoRoot, run, schema, serve, shells, tmpRoot, toon, version, win } from './lib.mjs';

const escape = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

function corrupted() {
  const dir = path.join(tmpRoot, 'dist-bad');
  fs.rmSync(dir, { recursive: true, force: true });
  fs.cpSync(distDir, dir, { recursive: true });
  for (const sub of ['latest/download', `download/${'v' + version}`, 'download/edge']) {
    const file = path.join(dir, sub, asset);
    const bytes = fs.readFileSync(file);
    bytes[bytes.length >> 1] ^= 0xff;
    fs.writeFileSync(file, bytes);
  }
  return dir;
}

async function installed(dir, msg) {
  const box = new Box('probe');
  const res = await run(path.join(dir, `hand${exe}`), ['version'], { env: box.env({}, []) });
  const v = toon(res.out).fields;
  equal([v.version, v.channel, v.commit, v.schema, v.luvus], [version, 'stable', commit(), String(schema), luvusTested], `${msg}: hand version reports the candidate`);
}

async function one(shell, good, bad) {
  const box = new Box(`install-${shell}`).prepare();
  const dir = path.join(box.dir, 'Hand Dir', 'bin');
  const target = path.join(dir, `hand${exe}`);
  const before = win ? await userPath(shell) : '';

  try {
    const first = await ok(await install(shell, good.base, dir, {}, box), `${shell}: the installer exits 0`);
    matches(first.out, new RegExp(`^installed hand version: ${escape(version)} to ${escape(target)}\\r?$`, 'm'), `${shell}: it reports the version and the path`);
    const asked = good.requests.splice(0);
    check(asked.includes(`/latest/download/${asset}`) && asked.some((r) => r.startsWith('/latest/download/') && r.endsWith(win ? '.sha256' : 'checksums.txt')), `${shell}: it fetches the archive and its checksums from the latest release`);
    if (win) {
      matches(first.out, new RegExp(`^added ${escape(dir)} to your user PATH; open a new terminal to use it\\r?$`, 'm'), `${shell}: it adds the folder to the user PATH and says so`);
      const listed = (await userPath(shell)).split(';').filter((p) => p.replace(/\\$/, '') === dir);
      equal(listed.length, 1, `${shell}: the user PATH holds the folder once`);
    } else {
      matches(first.out, new RegExp(`^${escape(dir)} is not on PATH; add this line to your shell profile: export PATH="${escape(dir)}:\\$PATH"$`, 'm'), `${shell}: it prints the PATH hint`);
    }
    await installed(dir, shell);

    const again = await ok(await install(shell, good.base, dir, {}, box, [dir]), `${shell}: a second install over the first exits 0`);
    if (win) {
      check(!/added .* to your user PATH/.test(again.out), `${shell}: the second install does not add the folder again`);
      await installed(dir, `${shell} again`);
      await cleaned(dir, () => installed(dir, `${shell} probe`));
      equal(fs.readdirSync(dir), [`hand${exe}`], `${shell}: the next hand run removed the old executable`);
    } else {
      check(!/is not on PATH/.test(again.out), `${shell}: no PATH hint when the folder is on PATH`);
      equal(fs.readdirSync(dir), ['hand'], `${shell}: the folder holds only hand, with no temporary file`);
    }

    for (const [name, extra, prefix] of [['a pinned version', { HAND_INSTALL_VERSION: `v${version}` }, `/download/v${version}/`], ['the edge channel', { HAND_INSTALL_VERSION: 'edge' }, '/download/edge/']]) {
      const other = path.join(box.dir, name.replace(/\W+/g, '-'), 'bin');
      await ok(await install(shell, good.base, other, extra, box), `${shell}: ${name} installs`);
      check(good.requests.splice(0).some((r) => r.startsWith(prefix) && r.endsWith(asset)), `${shell}: ${name} is fetched from ${prefix}`);
      await installed(other, `${shell}: ${name}`);
    }

    const refused = path.join(box.dir, 'refused', 'bin');
    const res = await install(shell, bad.base, refused, {}, box);
    await fails(res, `${shell}: a corrupted archive makes the installer exit 1`, 1);
    if (win) {
      const file = await run(shell, ['-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-File', path.join(distDir, 'install.ps1'), '-Dir', refused, '-Base', bad.base], { env: cleanEnv() });
      equal(file.code, 1, `${shell}: install.ps1 -File exits 1 on a corrupted archive`);
      matches(file.out + file.err, new RegExp(`install\\.ps1: checksum mismatch for ${escape(asset)}`), `${shell}: install.ps1 -File names the checksum mismatch`);
    }
    matches(res.out + res.err, new RegExp(`install\\.(sh|ps1): checksum mismatch for ${escape(asset)}`), `${shell}: it names the checksum mismatch`);
    check(!fs.existsSync(path.join(refused, `hand${exe}`)), `${shell}: nothing is installed after the mismatch`);

    if (!win) {
      const unfetched = await install(shell, `${good.base}/nope`, refused, {}, box);
      await fails(unfetched, `${shell}: a failed download of install.sh makes the documented command exit non-zero`, 22);
      check(!fs.existsSync(path.join(refused, `hand${exe}`)), `${shell}: nothing is installed after a failed download of install.sh`);
    }

    if (win) check(!fs.existsSync(path.join(process.env.LOCALAPPDATA, 'hand')), `${shell}: HAND_INSTALL_DIR won over the default folder`);
    else equal(fs.readdirSync(box.home), [], `${shell}: the installer left nothing in the home folder`);
    if (win) equal(fs.readdirSync(os.tmpdir()).filter((n) => /^hand-install-|^\.hand\./.test(n)), [], `${shell}: the installer left no temporary file`);
    else equal(fs.readdirSync(box.tmp), [], `${shell}: the installer left no temporary file`);
    if (win) {
      await setUserPath(shell, before);
      equal(await userPath(shell), before, `${shell}: the user PATH is back as it was`);
      const left = await run(shell, ['-NoProfile', '-NonInteractive', '-Command', "[Environment]::GetEnvironmentVariable('HAND_INSTALL_REFRESH','User')"], { env: cleanEnv() });
      equal(left.out.trim(), '', `${shell}: no HAND_INSTALL_REFRESH variable is left in the user environment`);
    }
  } finally {
    if (win) await setUserPath(shell, before);
  }
  if (win) equal(await userPath(shell), before, `${shell}: the user PATH is back as it was`);
}

await phase('install', async () => {
  const good = await serve(distDir);
  const badDir = corrupted();
  const bad = await serve(badDir);
  try {
    for (const shell of shells) await one(shell, good, bad);
    for (const n of ['install.sh', 'install.ps1']) check(fs.readFileSync(path.join(distDir, n), 'utf8') === fs.readFileSync(path.join(repoRoot, n), 'utf8').replace(/\r\n/g, '\n'), `${n} served from dist is the file in the repository`);
    await candidate();
  } finally {
    await good.close();
    await bad.close();
  }
});
