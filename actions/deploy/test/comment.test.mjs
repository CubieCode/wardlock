// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT
//
// The comment step against a fake GitHub API (fake-github.mjs).
import { afterEach, describe, expect, it } from 'vitest';

import { startFakeGitHub } from './fake-github.mjs';
import { runStep } from './helpers.mjs';

const token = 'ghs_fakeTokenForTheCommentTests';

/** @type {Awaited<ReturnType<typeof startFakeGitHub>> | undefined} */
let fake;
afterEach(async () => {
  await fake?.close();
  fake = undefined;
});

/** @param {Record<string, string>} env */
const comment = (env) =>
  runStep('comment.sh', {
    GITHUB_TOKEN: token,
    GITHUB_API: fake?.url ?? '',
    REPOSITORY: 'example/repo',
    PR_NUMBER: '42',
    MODE: 'deploy',
    SITE: 'docs',
    PREVIEW: 'pr-42',
    URL: 'https://docs--pr-42.sites.example.test/',
    DEPLOY_ID: '01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9e10',
    COMMIT_SHA: 'a'.repeat(40),
    ...env,
  });

describe('comment.sh', () => {
  it('creates one comment, then updates it in place', async () => {
    fake = await startFakeGitHub({ token });
    const first = await comment({});
    expect(first.status, first.stdout + first.stderr).toBe(0);
    expect(fake.comments).toHaveLength(1);
    const [c] = fake.comments;
    expect(c?.issue).toBe(42);
    expect(c?.body).toContain(
      '<!-- wardlock-preview site=docs preview=pr-42 -->',
    );
    expect(c?.body).toContain('https://docs--pr-42.sites.example.test/');
    expect(c?.body).toContain('`01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9e10`');
    expect(c?.body).toContain('a'.repeat(40));

    const second = await comment({
      DEPLOY_ID: 'second-deploy',
      COMMIT_SHA: 'b'.repeat(40),
    });
    expect(second.status, second.stdout + second.stderr).toBe(0);
    expect(fake.comments).toHaveLength(1);
    expect(fake.comments[0]?.id).toBe(c?.id);
    expect(fake.comments[0]?.updates).toBe(1);
    expect(fake.comments[0]?.body).toContain('`second-deploy`');
    expect(fake.comments[0]?.body).not.toContain('a'.repeat(40));
    expect(fake.requests.map((r) => r.method)).toEqual([
      'GET',
      'POST',
      'GET',
      'PATCH',
    ]);
  });

  it('keeps separate comments per pull request and per preview', async () => {
    fake = await startFakeGitHub({ token });
    await comment({});
    await comment({ PR_NUMBER: '43', PREVIEW: 'pr-43' });
    await comment({ SITE: 'blog' });
    expect(fake.comments.map((c) => [c.issue, c.updates])).toEqual([
      [42, 0],
      [43, 0],
      [42, 0],
    ]);
  });

  it("never edits a person's comment that copies the marker", async () => {
    fake = await startFakeGitHub({ token });
    fake.comments.push({
      id: 1,
      issue: 42,
      body: '<!-- wardlock-preview site=docs preview=pr-42 --> mine',
      user: { login: 'someone', type: 'User' },
      updates: 0,
    });
    const res = await comment({});
    expect(res.status).toBe(0);
    expect(fake.comments[0]?.updates).toBe(0);
    expect(fake.comments).toHaveLength(2);
  });

  it('finds its comment past the first page', async () => {
    fake = await startFakeGitHub({ token });
    for (let i = 0; i < 150; i++) {
      fake.comments.push({
        id: i + 1,
        issue: 42,
        body: `comment ${i}`,
        user: { login: 'someone', type: 'User' },
        updates: 0,
      });
    }
    await comment({});
    await comment({ DEPLOY_ID: 'again' });
    expect(fake.comments).toHaveLength(151);
    expect(fake.comments[150]?.updates).toBe(1);
  });

  it('marks the comment when the preview is deleted, and never creates one then', async () => {
    fake = await startFakeGitHub({ token });
    const none = await comment({ MODE: 'delete' });
    expect(none.status).toBe(0);
    expect(fake.comments).toHaveLength(0);

    await comment({});
    const res = await comment({ MODE: 'delete' });
    expect(res.status).toBe(0);
    expect(fake.comments).toHaveLength(1);
    expect(fake.comments[0]?.body).toContain('was deleted');
  });

  it('ends with a notice without pull-requests write', async () => {
    fake = await startFakeGitHub({ token, forbid: true });
    const res = await comment({});
    expect(res.status).toBe(0);
    expect(res.stdout).toContain('::notice');
    expect(res.stdout).toContain('pull-requests: write');
    expect(fake.comments).toHaveLength(0);
  });

  it('ends with a notice without a token', async () => {
    fake = await startFakeGitHub({ token });
    const res = await comment({ GITHUB_TOKEN: '' });
    expect(res.status).toBe(0);
    expect(res.stdout).toContain('::notice');
    expect(fake.requests).toHaveLength(0);
  });

  it('warns but does not fail when the API errs', async () => {
    fake = await startFakeGitHub({ token: 'another-token' });
    const res = await comment({});
    expect(res.status).toBe(0);
    expect(res.stdout).toContain('::warning');
    expect(res.stdout + res.stderr).not.toContain(token);
  });
});
