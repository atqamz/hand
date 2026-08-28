#!/usr/bin/env node
// Exercises lib.js's pure decision logic directly, with no filesystem or
// npm install involved. Run with `node lib.test.js`; exits nonzero on any
// failed assertion. tests/npmpublish drives this from `go test`. Lives outside
// bin/ so the meta package's "files": ["bin/"] never ships it.
'use strict';

const assert = require('assert');
const { resolvePlatformPackage, resolveBinaryPath } = require('./bin/lib.js');

// A platform npm never lists as an optional dependency of this release gets a
// platform-specific refusal, not a generic "package missing" message - see
// docs/adr/npm-publishes-only-runtime-qualified-targets-behind-one-operator-gate.md.
{
  const r = resolvePlatformPackage({
    platform: 'darwin',
    arch: 'arm64',
    optionalDependencies: { '@atqamz/hand-linux-x64': '0.7.0' },
  });
  assert.strictEqual(r.ok, false);
  assert.match(r.message, /macOS is not supported/);
  assert.match(r.message, /github\.com\/atqamz\/hand\/releases/);
}

{
  const r = resolvePlatformPackage({
    platform: 'darwin',
    arch: 'x64',
    optionalDependencies: {},
  });
  assert.strictEqual(r.ok, false);
  assert.match(r.message, /macOS is not supported/);
}

// A platform that is simply not part of this release's optional dependencies
// (and is not macOS) gets a generic, still-actionable refusal naming exactly
// what was asked for.
{
  const r = resolvePlatformPackage({
    platform: 'linux',
    arch: 'arm64',
    optionalDependencies: { '@atqamz/hand-linux-x64': '0.7.0' },
  });
  assert.strictEqual(r.ok, false);
  assert.doesNotMatch(r.message, /macOS/);
  assert.match(r.message, /linux\/arm64/);
}

{
  const r = resolvePlatformPackage({ platform: 'linux', arch: 'x64', optionalDependencies: undefined });
  assert.strictEqual(r.ok, false);
}

// A platform this release actually publishes resolves to its optional
// dependency name and the right binary name for its OS.
{
  const r = resolvePlatformPackage({
    platform: 'linux',
    arch: 'x64',
    optionalDependencies: { '@atqamz/hand-linux-x64': '0.7.0', '@atqamz/hand-win32-x64': '0.7.0' },
  });
  assert.deepStrictEqual(r, { ok: true, depName: '@atqamz/hand-linux-x64', binName: 'hand' });
}

{
  const r = resolvePlatformPackage({
    platform: 'win32',
    arch: 'x64',
    optionalDependencies: { '@atqamz/hand-win32-x64': '0.7.0' },
  });
  assert.deepStrictEqual(r, { ok: true, depName: '@atqamz/hand-win32-x64', binName: 'hand.exe' });
}

// The platform is listed, but its optional dependency was not actually
// installed (e.g. --no-optional, or an npm resolution that skipped it) - a
// distinct failure from "this release does not publish your platform".
{
  const r = resolveBinaryPath('@atqamz/hand-linux-x64', 'hand', () => {
    throw new Error('MODULE_NOT_FOUND');
  });
  assert.strictEqual(r.ok, false);
  assert.match(r.message, /@atqamz\/hand-linux-x64/);
  assert.match(r.message, /npm install -g @atqamz\/hand/);
}

// A successfully resolved platform package joins its package.json directory
// with bin/<binName> - proven with a fake resolver, no real node_modules.
{
  let asked;
  const r = resolveBinaryPath('@atqamz/hand-linux-x64', 'hand', (request) => {
    asked = request;
    return '/fake/node_modules/@atqamz/hand-linux-x64/package.json';
  });
  assert.strictEqual(asked, '@atqamz/hand-linux-x64/package.json');
  assert.strictEqual(r.ok, true);
  assert.strictEqual(r.path, require('path').join('/fake/node_modules/@atqamz/hand-linux-x64', 'bin', 'hand'));
}

console.log('lib.test.js: all assertions passed');
