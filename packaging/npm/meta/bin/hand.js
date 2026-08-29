#!/usr/bin/env node
'use strict';

const { spawnSync } = require('child_process');
const { resolvePlatformPackage, resolveBinaryPath } = require('./lib.js');

const pkg = require('../package.json');

const platformResult = resolvePlatformPackage({
  platform: process.platform,
  arch: process.arch,
  optionalDependencies: pkg.optionalDependencies,
});
if (!platformResult.ok) {
  process.stderr.write(platformResult.message);
  process.exit(1);
}

const binaryResult = resolveBinaryPath(platformResult.depName, platformResult.binName, require.resolve);
if (!binaryResult.ok) {
  process.stderr.write(binaryResult.message);
  process.exit(1);
}

const result = spawnSync(binaryResult.path, process.argv.slice(2), { stdio: 'inherit' });

if (result.error) {
  process.stderr.write(`hand: failed to run ${binaryResult.path}: ${result.error.message}\n`);
  process.exit(1);
}

process.exit(result.status === null ? 1 : result.status);
