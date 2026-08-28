'use strict';

const path = require('path');

const DARWIN_MESSAGE =
  'hand: macOS is not supported by this npm release.\n' +
  '\n' +
  '@atqamz/hand only ships prebuilt binaries here for targets whose private runtime is\n' +
  'qualified for this release, and macOS is not one of them yet.\n' +
  '\n' +
  'hand does run on macOS - just not installed through npm right now. Get a native build\n' +
  'from the GitHub release instead:\n' +
  '\n' +
  '  https://github.com/atqamz/hand/releases\n' +
  '\n' +
  '(the bootstrap script linked there installs it directly, no compiler needed.)\n';

function unsupportedPlatformMessage(platform, arch) {
  if (platform === 'darwin') {
    return DARWIN_MESSAGE;
  }
  return (
    `hand: the npm distribution of @atqamz/hand does not publish a prebuilt binary for ` +
    `${platform}/${arch} in this release.\n` +
    `Check https://github.com/atqamz/hand/releases for a native build, or see\n` +
    `https://github.com/atqamz/hand for current platform support.\n`
  );
}

function missingOptionalDependencyMessage(depName) {
  return (
    `hand: no prebuilt binary for this platform (optional dependency ${depName} was not\n` +
    `installed). Reinstall with optional dependencies enabled, e.g.\n` +
    `\`npm install -g @atqamz/hand\`.\n`
  );
}

// Decides which platform package this install should run, using only this package's own
// optionalDependencies as the list of what this release actually publishes - never a
// second, hand-maintained platform list that could drift from it.
function resolvePlatformPackage({ platform, arch, optionalDependencies }) {
  const depName = `@atqamz/hand-${platform}-${arch}`;
  if (!optionalDependencies || !Object.prototype.hasOwnProperty.call(optionalDependencies, depName)) {
    return { ok: false, message: unsupportedPlatformMessage(platform, arch) };
  }
  const binName = platform === 'win32' ? 'hand.exe' : 'hand';
  return { ok: true, depName, binName };
}

// resolveFn is injected as require.resolve, so a test can fake module resolution
// without needing a real node_modules layout on disk.
function resolveBinaryPath(depName, binName, resolveFn) {
  let pkgPath;
  try {
    pkgPath = resolveFn(`${depName}/package.json`);
  } catch {
    return { ok: false, message: missingOptionalDependencyMessage(depName) };
  }
  return { ok: true, path: path.join(path.dirname(pkgPath), 'bin', binName) };
}

module.exports = { resolvePlatformPackage, resolveBinaryPath };
