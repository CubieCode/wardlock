// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// The CLI's integration test for C1.14: the built `wardlock` deploys a
// fixture site to the real server, pulls it back and gets the same bytes.
import { execFile } from 'node:child_process';
import { mkdtempSync, readdirSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';
import { expect, test } from '@playwright/test';
import { firstOwnerState } from './mail.js';

const origin = process.env.WARDLOCK_ACCEPTANCE_ORIGIN;
const wardlockd = process.env.WARDLOCK_ACCEPTANCE_WARDLOCKD;
if (!origin || !wardlockd) throw new Error('Run through pnpm browser:test');
// pnpm build puts the CLI beside wardlockd.
const wardlock = join(dirname(wardlockd), 'wardlock');
const fixture = resolve(
  dirname(fileURLToPath(import.meta.url)),
  '../../../fixtures/sites/basic',
);
const run = promisify(execFile);
// Signed in as the first owner, whose session setup saved: email-code sends
// are limited per IP, and the other specs use them.
test.use({ storageState: firstOwnerState });

/** Every file under dir, by slash path, with its bytes. */
function tree(dir: string) {
  const files = new Map<string, Buffer>();
  for (const entry of readdirSync(dir, {
    recursive: true,
    withFileTypes: true,
  })) {
    if (entry.isFile()) {
      const path = join(entry.parentPath, entry.name);
      files.set(relative(dir, path).split('\\').join('/'), readFileSync(path));
    }
  }
  return files;
}

test('CLI deploy then pull gives back the same files', async ({
  page,
}, info) => {
  const site = `pull-${info.project.name}`;
  const created = await page.request.post(`${origin}/api/v0/tokens`, {
    data: { name: `pull acceptance ${info.project.name}`, scopes: ['deploy'] },
    headers: { Origin: origin },
  });
  expect(created.status()).toBe(201);
  const { token, apiToken } = (await created.json()) as {
    token: string;
    apiToken: { id: string };
  };
  const work = mkdtempSync(join(tmpdir(), 'wardlock-pull-'));
  // A throwaway home, so no stored login or keychain is touched.
  const env = {
    ...process.env,
    HOME: work,
    XDG_CONFIG_HOME: work,
    WARDLOCK_SERVER: origin,
    WARDLOCK_TOKEN: token,
  };
  const cli = (...args: string[]) => run(wardlock, args, { cwd: work, env });
  try {
    const deployed = await cli('deploy', fixture, site, '--json');
    const { deploy } = JSON.parse(deployed.stdout) as {
      deploy: { id: string };
    };

    const pulled = await cli('pull', site, 'copy', '--json');
    const result = JSON.parse(pulled.stdout) as {
      site: { name: string };
      deploy: { id: string };
      dir: string;
      files: number;
      bytes: number;
    };
    const want = tree(fixture);
    const got = tree(join(work, 'copy'));
    expect([...got.keys()].sort()).toEqual([...want.keys()].sort());
    for (const [path, bytes] of want) {
      expect(got.get(path)?.equals(bytes), path).toBe(true);
    }
    expect(result.site.name).toBe(site);
    expect(result.deploy.id).toBe(deploy.id);
    expect(result.dir).toBe(join(work, 'copy'));
    expect(result.files).toBe(want.size);
    expect(result.bytes).toBe(
      [...want.values()].reduce((n, b) => n + b.length, 0),
    );
    for (const out of [deployed, pulled]) {
      expect(out.stdout + out.stderr).not.toContain(token);
    }

    // A second pull into the same directory is refused without --force.
    await expect(cli('pull', site, 'copy')).rejects.toMatchObject({
      code: 1,
      stderr: expect.stringContaining('copy is not empty'),
    });
  } finally {
    rmSync(work, { recursive: true, force: true });
    await page.request.delete(`${origin}/api/v0/tokens/${apiToken.id}`, {
      headers: { Origin: origin },
    });
  }
});
