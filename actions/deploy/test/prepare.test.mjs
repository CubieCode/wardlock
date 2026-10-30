// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT
//
// The step that masks the token and decides between deploy, delete and skip.
import { describe, expect, it } from 'vitest';

import { runStep } from './helpers.mjs';

const token = 'wl_k7m2q6xw4a_EXAMPLEonlyEXAMPLEonlyEXAMPLEonly';

/** @param {Record<string, string>} env */
const prepare = (env) =>
  runStep('prepare.sh', {
    WARDLOCK_TOKEN: token,
    EVENT_NAME: 'push',
    REPOSITORY: 'example/repo',
    ACTOR: 'someone',
    INPUT_DELETE: 'false',
    INPUT_PR_PREVIEW: 'true',
    ...env,
  });

/** @param {Record<string, string>} env */
const pullRequest = (env) =>
  prepare({
    EVENT_NAME: 'pull_request',
    EVENT_ACTION: 'synchronize',
    PR_NUMBER: '42',
    PR_HEAD_REPO: 'example/repo',
    ...env,
  });

describe('prepare.sh', () => {
  it('masks the token before anything else', async () => {
    const res = await prepare({});
    expect(res.status).toBe(0);
    expect(res.stdout.split('\n')[0]).toBe(`::add-mask::${token}`);
  });

  it('deploys to the live site on a push', async () => {
    const res = await prepare({});
    expect(res.outputs).toEqual({ mode: 'deploy', preview: '' });
  });

  it('deploys to pr-<number> on a pull request', async () => {
    const res = await pullRequest({});
    expect(res.outputs).toEqual({ mode: 'deploy', preview: 'pr-42' });
  });

  it('deploys live on a pull request with pr-preview false', async () => {
    const res = await pullRequest({ INPUT_PR_PREVIEW: 'false' });
    expect(res.outputs).toEqual({ mode: 'deploy', preview: '' });
    const closed = await pullRequest({
      INPUT_PR_PREVIEW: 'false',
      EVENT_ACTION: 'closed',
    });
    expect(closed.outputs).toEqual({ mode: 'deploy', preview: '' });
  });

  it('prefers the preview input', async () => {
    const res = await pullRequest({ INPUT_PREVIEW: 'redesign' });
    expect(res.outputs).toEqual({ mode: 'deploy', preview: 'redesign' });
  });

  it('deletes the preview when the pull request closes', async () => {
    const res = await pullRequest({ EVENT_ACTION: 'closed' });
    expect(res.outputs).toEqual({ mode: 'delete', preview: 'pr-42' });
  });

  it('deletes on request', async () => {
    const res = await prepare({ INPUT_DELETE: 'true', INPUT_PREVIEW: 'ci' });
    expect(res.outputs).toEqual({ mode: 'delete', preview: 'ci' });
  });

  it('refuses to delete without a preview', async () => {
    const res = await prepare({ INPUT_DELETE: 'true' });
    expect(res.status).toBe(1);
    expect(res.stdout).toContain('delete needs a preview');
  });

  it('refuses boolean inputs other than true or false', async () => {
    const del = await prepare({ INPUT_DELETE: 'yes' });
    expect(del.status).toBe(1);
    expect(del.stdout).toContain('delete must be true or false');
    const pr = await prepare({ INPUT_PR_PREVIEW: '1' });
    expect(pr.status).toBe(1);
    expect(pr.stdout).toContain('pr-preview must be true or false');
  });

  it('ends with a notice for a pull request from a fork', async () => {
    const res = await pullRequest({
      WARDLOCK_TOKEN: '',
      PR_HEAD_REPO: 'fork/repo',
    });
    expect(res.status).toBe(0);
    expect(res.stdout).toContain('::notice title=Wardlock deploy skipped::');
    expect(res.outputs).toEqual({ mode: 'skip', preview: '' });
  });

  it('ends with a notice for Dependabot', async () => {
    const res = await pullRequest({
      WARDLOCK_TOKEN: '',
      ACTOR: 'dependabot[bot]',
    });
    expect(res.status).toBe(0);
    expect(res.outputs.mode).toBe('skip');
  });

  it('fails without a token otherwise', async () => {
    const pr = await pullRequest({ WARDLOCK_TOKEN: '' });
    expect(pr.status).toBe(1);
    expect(pr.stdout).toContain('token is empty');
    const push = await prepare({ WARDLOCK_TOKEN: '' });
    expect(push.status).toBe(1);
  });

  it('warns on pull_request_target', async () => {
    const res = await prepare({ EVENT_NAME: 'pull_request_target' });
    expect(res.stdout).toContain('::warning');
  });
});
