// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT
//
// Runs deploy/install.sh against a synthetic release directory served from
// file:// URLs. The real snapshot archives are covered by
// scripts/release-smoke.sh in the release workflow.
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import {
  chmodSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';

const script = resolve(import.meta.dirname, '../deploy/install.sh');
const os = process.platform;
/** @type {Record<string, string>} */
const goarch = { x64: 'amd64', arm64: 'arm64' };
const arch = goarch[process.arch];
const supported = (os === 'linux' || os === 'darwin') && arch !== undefined;
const version = '1.2.3';

/** @type {string} */
let root;
/** @type {string} */
let release;
/** @type {string} */
let dest;

/** Builds <bin>_<version>_<os>_<arch>.tar.gz holding a fake binary. */
function archive(/** @type {string} */ bin) {
  const src = join(root, `src-${bin}`);
  mkdirSync(src);
  writeFileSync(join(src, bin), `#!/bin/sh\necho ${bin} ${version}\n`);
  chmodSync(join(src, bin), 0o755);
  writeFileSync(join(src, 'README.md'), 'synthetic\n');
  const name = `${bin}_${version}_${os}_${arch}.tar.gz`;
  const tar = spawnSync(
    'tar',
    ['-czf', join(release, name), '-C', src, bin, 'README.md'],
    {
      encoding: 'utf8',
    },
  );
  expect(tar.status, tar.stderr).toBe(0);
  return name;
}

const sha256 = (/** @type {string} */ path) =>
  createHash('sha256').update(readFileSync(path)).digest('hex');

function writeChecksums(/** @type {string[]} */ names) {
  writeFileSync(
    join(release, 'checksums.txt'),
    names.map((n) => `${sha256(join(release, n))}  ${n}\n`).join(''),
  );
}

function install(/** @type {string[]} */ args) {
  return spawnSync(
    'sh',
    [script, '--base-url', `file://${release}`, '--dir', dest, ...args],
    { encoding: 'utf8', env: { PATH: process.env.PATH ?? '' } },
  );
}

describe.runIf(supported)('deploy/install.sh', () => {
  beforeEach(() => {
    root = mkdtempSync(join(tmpdir(), 'wardlock-install-'));
    release = join(root, 'release');
    dest = join(root, 'bin');
    mkdirSync(release);
  });
  afterEach(() => rmSync(root, { recursive: true, force: true }));

  it('installs both binaries after verifying their checksums', () => {
    writeChecksums([archive('wardlock'), archive('wardlockd')]);
    const res = install(['--version', `v${version}`, '--component', 'all']);
    expect(res.status, res.stderr).toBe(0);
    expect(res.stderr).toContain(`verified wardlock_${version}_${os}_${arch}`);
    for (const bin of ['wardlock', 'wardlockd']) {
      const run = spawnSync(join(dest, bin), { encoding: 'utf8' });
      expect(run.stdout).toBe(`${bin} ${version}\n`);
    }
    // Only the binary is installed, not the rest of the archive.
    expect(existsSync(join(dest, 'README.md'))).toBe(false);
  });

  it('refuses an archive whose checksum does not match', () => {
    const name = archive('wardlock');
    writeChecksums([name]);
    writeFileSync(join(release, name), 'tampered');
    const res = install(['--version', version]);
    expect(res.status).not.toBe(0);
    expect(res.stderr).toContain('checksum mismatch');
    expect(existsSync(join(dest, 'wardlock'))).toBe(false);
  });

  it('refuses an archive missing from checksums.txt', () => {
    archive('wardlock');
    writeFileSync(join(release, 'checksums.txt'), '');
    const res = install(['--version', version]);
    expect(res.status).not.toBe(0);
    expect(res.stderr).toContain('not listed in checksums.txt');
    expect(existsSync(join(dest, 'wardlock'))).toBe(false);
  });

  it('does not match a checksum line for a different file name', () => {
    const name = archive('wardlock');
    writeFileSync(
      join(release, 'checksums.txt'),
      `${sha256(join(release, name))}  evil-${name}\n`,
    );
    const res = install(['--version', version]);
    expect(res.status).not.toBe(0);
    expect(existsSync(join(dest, 'wardlock'))).toBe(false);
  });

  it('rejects unknown components and unsafe versions', () => {
    expect(install(['--version', version, '--component', 'x']).status).toBe(1);
    const res = install(['--version', '1.0.0/../../x']);
    expect(res.status).toBe(1);
    expect(res.stderr).toContain('invalid version');
  });

  it('needs a version when given a base URL', () => {
    const res = install([]);
    expect(res.status).toBe(1);
    expect(res.stderr).toContain('--base-url needs --version');
  });
});
