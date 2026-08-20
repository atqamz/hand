#!/usr/bin/env node
'use strict';

const path = require('path');
const { spawnSync } = require('child_process');

const BIN_NAME = {
  'linux-x64': 'hand',
  'linux-arm64': 'hand',
  'darwin-x64': 'hand',
  'darwin-arm64': 'hand',
  'win32-x64': 'hand.exe',
};

const platform = `${process.platform}-${process.arch}`;
const binName = BIN_NAME[platform];

if (!binName) {
  process.stderr.write(
    `hand: unsupported platform ${process.platform}/${process.arch}\n`
  );
  process.exit(1);
}

let pkgPath;
try {
  pkgPath = require.resolve(`@atqamz/hand-${platform}/package.json`);
} catch {
  process.stderr.write(
    `hand: no prebuilt binary for ${process.platform}/${process.arch} ` +
      `(optional dependency @atqamz/hand-${platform} was not installed)\n`
  );
  process.exit(1);
}

const binPath = path.join(path.dirname(pkgPath), 'bin', binName);
const result = spawnSync(binPath, process.argv.slice(2), { stdio: 'inherit' });

if (result.error) {
  process.stderr.write(`hand: failed to run ${binPath}: ${result.error.message}\n`);
  process.exit(1);
}

process.exit(result.status === null ? 1 : result.status);
