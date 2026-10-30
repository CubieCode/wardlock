// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT
//
// The deploy step with a stand-in wardlock binary that records its
// arguments and prints canned --json output. The action's CI job runs the
// real CLI against a throwaway wardlockd.
import { chmodSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { beforeEach, describe, expect, it } from 'vitest';

import { runStep, tempDir } from './helpers.mjs';

const token = 'wl_k7m2q6xw4a_EXAMPLEonlyEXAMPLEonlyEXAMPLEonly';
const sha = '0123456789abcdef0123456789abcdef01234567';
/** @type {string} */
let dir;

const site = {
  name: 'docs',
  hostname: 'docs.sites.example.test',
  url: 'https://docs.sites.example.test/',
};
const activation = { site, deploy: { id: 'deploy-1', status: 'active' } };

/**
 * Writes the stand-in CLI: it records argv and whether it saw the token,
 * then prints `stdout` and `stderr` and exits with `status`.
 * @param {{ stdout?: unknown, stderr?: string, status?: number }} result
 */
function fakeCLI({ stdout = activation, stderr = '', status = 0 }) {
  const cli = join(dir, 'wardlock');
  writeFileSync(join(dir, 'stdout.json'), JSON.stringify(stdout));
  writeFileSync(join(dir, 'stderr.txt'), stderr);
  writeFileSync(
    cli,
    `#!/usr/bin/env bash
printf '%s\\n' "$@" > '${dir}/args'
[[ $WARDLOCK_TOKEN == '${token}' ]] && echo yes > '${dir}/token-seen'
cat '${dir}/stderr.txt' >&2
[[ ${status} == 0 ]] && cat '${dir}/stdout.json'
exit ${status}
`,
  );
  chmodSync(cli, 0o755);
  return cli;
}

const args = () =>
  readFileSync(join(dir, 'args'), 'utf8').trimEnd().split('\n');

/**
 * @param {Record<string, string>} env
 * @param {{ stdout?: unknown, stderr?: string, status?: number }} [result]
 */
const deploy = (env, result = {}) =>
  runStep(
    'deploy.sh',
    {
      WARDLOCK_TOKEN: token,
      WARDLOCK_CLI: fakeCLI(result),
      MODE: 'deploy',
      PREVIEW: '',
      COMMIT_SHA: sha,
      COMMIT_FALLBACK_SUBJECT: 'Fix the install page\n\nLonger body',
      ...env,
    },
    { cwd: dir },
  );

beforeEach(() => {
  dir = tempDir();
});

describe('deploy.sh', () => {
  it('deploys with wardlock.json and a default message', async () => {
    const res = await deploy({});
    expect(res.status, res.stdout + res.stderr).toBe(0);
    expect(args()).toEqual([
      '--json',
      'deploy',
      '--message',
      '0123456 Fix the install page',
    ]);
    expect(readFileSync(join(dir, 'token-seen'), 'utf8')).toBe('yes\n');
    expect(res.outputs).toEqual({
      site: 'docs',
      'deploy-id': 'deploy-1',
      url: 'https://docs.sites.example.test/',
    });
    expect(res.stdout).toContain('::notice title=Wardlock deployed::');
    expect(res.stdout + res.stderr).not.toContain(token);
  });

  it('passes every input to the CLI', async () => {
    const res = await deploy(
      {
        INPUT_SERVER: 'https://app.example.test',
        INPUT_DIRECTORY: 'dist',
        INPUT_SITE: 'docs',
        INPUT_MESSAGE: 'docs 0123456789ab',
        INPUT_PATH: '/mockup-x/',
        PREVIEW: 'pr-42',
      },
      {
        stdout: {
          ...activation,
          path: '/mockup-x/',
          preview: 'pr-42',
          url: 'https://docs--pr-42.sites.example.test/mockup-x/',
        },
      },
    );
    expect(res.status, res.stdout + res.stderr).toBe(0);
    expect(args()).toEqual([
      '--json',
      '--server',
      'https://app.example.test',
      'deploy',
      'dist',
      'docs',
      '--message',
      'docs 0123456789ab',
      '--path',
      '/mockup-x/',
      '--preview',
      'pr-42',
    ]);
    expect(res.outputs.url).toBe(
      'https://docs--pr-42.sites.example.test/mockup-x/',
    );
    expect(res.stdout).toContain('::notice title=Wardlock preview deployed::');
  });

  it('takes the directory from wardlock.json when only the site is given', async () => {
    writeFileSync(
      join(dir, 'wardlock.json'),
      JSON.stringify({ site: 'blog', dir: 'dist' }),
    );
    await deploy({ INPUT_SITE: 'docs' });
    expect(args().slice(0, 4)).toEqual(['--json', 'deploy', 'dist', 'docs']);
  });

  it('gives a live path deploy the address of its prefix', async () => {
    const res = await deploy(
      { INPUT_PATH: 'mockup-x' },
      { stdout: { ...activation, path: '/mockup-x/' } },
    );
    expect(res.outputs.url).toBe('https://docs.sites.example.test/mockup-x/');
  });

  it('fails when the CLI fails', async () => {
    const res = await deploy(
      {},
      { status: 1, stderr: 'wardlock: could not reach the server\n' },
    );
    expect(res.status).toBe(1);
    expect(res.stderr).toContain('could not reach the server');
    expect(res.stdout).toContain('::error');
    expect(res.outputs).toEqual({});
  });

  it('deletes a preview, with the site from wardlock.json', async () => {
    writeFileSync(
      join(dir, 'wardlock.json'),
      JSON.stringify({ site: 'docs', dir: 'dist' }),
    );
    const res = await deploy(
      { MODE: 'delete', PREVIEW: 'pr-42' },
      {
        stdout: { deleted: true, site, preview: 'pr-42' },
      },
    );
    expect(res.status, res.stdout + res.stderr).toBe(0);
    expect(args()).toEqual([
      '--json',
      'preview',
      'delete',
      'docs',
      'pr-42',
      '--yes',
    ]);
    expect(res.outputs).toEqual({ site: 'docs' });
    expect(res.stdout).toContain('Deleted the preview pr-42 of docs');
  });

  it('treats a preview that is already gone as deleted', async () => {
    const res = await deploy(
      { MODE: 'delete', PREVIEW: 'pr-42', INPUT_SITE: 'docs' },
      {
        status: 1,
        stderr:
          'wardlock: docs has no preview pr-42; wardlock preview list docs lists them\n',
      },
    );
    expect(res.status).toBe(0);
    expect(res.stdout).toContain('nothing to delete');
  });

  it('fails when a deletion fails otherwise', async () => {
    const res = await deploy(
      { MODE: 'delete', PREVIEW: 'pr-42', INPUT_SITE: 'docs' },
      {
        status: 1,
        stderr: 'wardlock: the token lacks the deploy scope\n',
      },
    );
    expect(res.status).toBe(1);
    expect(res.stderr).toContain('deploy scope');
  });

  it('needs a site to delete from', async () => {
    const res = await deploy({ MODE: 'delete', PREVIEW: 'pr-42' });
    expect(res.status).toBe(1);
    expect(res.stdout).toContain('which site?');
  });
});
