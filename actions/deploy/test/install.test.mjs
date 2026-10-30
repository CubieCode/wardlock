// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT
//
// The download step against a synthetic release directory on file://.
// The action's CI job also runs the whole action with a tampered archive.
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { chmodSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { beforeEach, describe, expect, it } from 'vitest';

import { runStep, tempDir } from './helpers.mjs';

const version = '1.2.3';
const archive = `wardlock_${version}_linux_amd64.tar.gz`;
/** @type {string} */
let release;

const sha256 = (/** @type {string} */ path) =>
  createHash('sha256').update(readFileSync(path)).digest('hex');

beforeEach(() => {
  const root = tempDir();
  release = join(root, 'release');
  const src = join(root, 'src');
  mkdirSync(release);
  mkdirSync(src);
  writeFileSync(join(src, 'wardlock'), `#!/bin/sh\necho wardlock ${version}\n`);
  chmodSync(join(src, 'wardlock'), 0o755);
  const tar = spawnSync(
    'tar',
    ['-czf', join(release, archive), '-C', src, 'wardlock'],
    {
      encoding: 'utf8',
    },
  );
  expect(tar.status, tar.stderr).toBe(0);
  writeFileSync(
    join(release, 'checksums.txt'),
    `${sha256(join(release, archive))}  ${archive}\n`,
  );
});

/** @param {Record<string, string>} env */
const install = (env) =>
  runStep('install.sh', {
    RUNNER_OS: 'Linux',
    RUNNER_ARCH: 'X64',
    WARDLOCK_ACTION_RELEASE_URL: `file://${release}`,
    ...env,
  });

describe('install.sh', () => {
  it('installs the verified binary at the version input', async () => {
    const res = await install({ INPUT_VERSION: `v${version}` });
    expect(res.status, res.stdout + res.stderr).toBe(0);
    expect(res.stdout).toContain(`Verified ${archive}`);
    const cli = res.outputs.cli ?? '';
    expect(cli).toMatch(/\/bin\/wardlock$/);
    expect(spawnSync(cli, { encoding: 'utf8' }).stdout).toBe(
      `wardlock ${version}\n`,
    );
  });

  it('uses the tag the action is used at', async () => {
    const res = await install({ ACTION_REF: `v${version}` });
    expect(res.status, res.stdout + res.stderr).toBe(0);
  });

  it('refuses a ref that is not a release tag without version', async () => {
    const res = await install({ ACTION_REF: 'main' });
    expect(res.status).toBe(1);
    expect(res.stdout).toContain('::error');
    expect(res.stdout).toContain('set version');
  });

  it('fails on a checksum mismatch', async () => {
    writeFileSync(
      join(release, 'checksums.txt'),
      `${'0'.repeat(64)}  ${archive}\n`,
    );
    const res = await install({ INPUT_VERSION: version });
    expect(res.status).toBe(1);
    expect(res.stdout).toContain(
      `::error title=Wardlock deploy::checksum mismatch for ${archive}`,
    );
    expect(res.outputs.cli).toBeUndefined();
  });

  it('fails when checksums.txt does not list the archive', async () => {
    writeFileSync(
      join(release, 'checksums.txt'),
      `${'0'.repeat(64)}  other.tar.gz\n`,
    );
    const res = await install({ INPUT_VERSION: version });
    expect(res.status).toBe(1);
    expect(res.stdout).toContain('is not listed in checksums.txt');
  });

  it('fails when the archive is missing', async () => {
    const res = await install({ INPUT_VERSION: version, RUNNER_ARCH: 'ARM64' });
    expect(res.status).toBe(1);
    expect(res.stdout).toContain('could not download');
  });

  it('refuses a plain http release URL', async () => {
    const res = await install({
      INPUT_VERSION: version,
      WARDLOCK_ACTION_RELEASE_URL: 'http://example.test/release',
    });
    expect(res.status).toBe(1);
    expect(res.stdout).toContain('https:// or file://');
  });
});
