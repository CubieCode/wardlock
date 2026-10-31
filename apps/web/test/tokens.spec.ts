// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// I6.6: an owner creates a deploy-only token on the page, the built CLI
// deploys with it, and once revoked the CLI gets 401. `wardlock token create`
// mints one through the same approval page.
import { execFile, spawn } from 'node:child_process';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';
import { expect, test, type Page, type TestInfo } from '@playwright/test';
import { firstOwnerState, schemes } from './mail.js';
import { goToPlace } from './places.js';

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

const tokenShape = /wl_[a-z2-7]{10}_[A-Za-z0-9_-]{43}/;

/** No sideways scroll, then a full-page screenshot with no token in it. */
async function check(page: Page, info: TestInfo, name: string) {
  const overflow = await page.evaluate(
    () =>
      document.documentElement.scrollWidth -
      document.documentElement.clientWidth,
  );
  expect(overflow).toBeLessThanOrEqual(0);
  expect(await page.content()).not.toMatch(tokenShape);
  expect(await page.title()).not.toMatch(/wl_/);
  await page.screenshot({
    path: info.outputPath(`${name}.png`),
    fullPage: true,
  });
}

for (const scheme of schemes) {
  test(`create, deploy with, then revoke an API token (${scheme})`, async ({
    page,
  }, info) => {
    await page.emulateMedia({ colorScheme: scheme });
    const shot = (step: string) =>
      check(page, info, `tokens-${step}-${scheme}`);
    const suffix = `${info.project.name}-${scheme}`;
    const name = `CI deploys ${suffix}`;
    const site = `token-${suffix}`;
    const work = mkdtempSync(join(tmpdir(), 'wardlock-token-'));
    let token = '';
    const cli = (...args: string[]) =>
      run(wardlock, args, {
        cwd: work,
        // A throwaway home, so no stored login or keychain is touched.
        env: {
          ...process.env,
          HOME: work,
          XDG_CONFIG_HOME: work,
          WARDLOCK_SERVER: origin,
          WARDLOCK_TOKEN: token,
        },
      });
    try {
      await test.step('the owner creates a deploy-only token', async () => {
        await page.goto(`${origin}/`);
        await goToPlace(page, info, 'API tokens');
        await expect(
          page.getByRole('heading', { level: 1, name: 'API tokens' }),
        ).toBeVisible();
        await page.getByRole('button', { name: 'Create token' }).click();
        const dialog = page.getByRole('dialog', {
          name: 'Create an API token',
        });
        await expect(
          dialog.getByRole('checkbox', { name: /Deploy/ }),
        ).toBeChecked();
        await expect(
          dialog.getByRole('checkbox', { name: /Manage/ }),
        ).not.toBeChecked();
        await expect(dialog.getByRole('combobox')).toHaveValue('90d');
        await dialog.getByRole('textbox', { name: /^Name/ }).fill(name);
        await shot('create');
        await dialog.getByRole('button', { name: 'Create token' }).click();
      });

      await test.step('it is shown once, then only by its public ID', async () => {
        const dialog = page.getByRole('dialog', {
          name: 'Copy your new token',
        });
        const field = dialog.getByRole('textbox', { name: 'API token' });
        await expect(field).toHaveValue(tokenShape);
        token = await field.inputValue();
        await expect(dialog.getByText(/cannot be shown again/)).toBeVisible();
        const overflow = await page.evaluate(
          () =>
            document.documentElement.scrollWidth -
            document.documentElement.clientWidth,
        );
        expect(overflow).toBeLessThanOrEqual(0);
        // A throwaway token on a throwaway server, gone by the end of the test.
        await page.screenshot({
          path: info.outputPath(`tokens-shown-once-${scheme}.png`),
        });
        await page.keyboard.press('Escape');
        await expect(field).toHaveValue(token);
        await dialog.getByRole('button', { name: 'Done' }).click();
        await expect(dialog).toBeHidden();
        const publicId = token.split('_')[1]!;
        await expect(page.getByText(publicId)).toBeVisible();
        await shot('list');
        await page.reload();
        await expect(page.getByText(publicId)).toBeVisible();
        expect(await page.content()).not.toMatch(tokenShape);
      });

      await test.step('the CLI deploys with it', async () => {
        const { stdout } = await cli('deploy', fixture, site, '--json');
        const { deploy } = JSON.parse(stdout) as { deploy: { id: string } };
        expect(deploy.id).toBeTruthy();
        await page.reload();
        const row = page.getByRole('row', { name: new RegExp(name) });
        await expect(row.getByText('just now')).toBeVisible();
      });

      await test.step('revoking it locks the CLI out', async () => {
        await page.getByRole('button', { name: `Revoke ${name}` }).click();
        const dialog = page.getByRole('dialog', {
          name: `Revoke “${name}”?`,
        });
        await shot('revoke');
        await dialog.getByRole('button', { name: 'Cancel' }).click();
        await expect(dialog).toBeHidden();
        await page.getByRole('button', { name: `Revoke ${name}` }).click();
        await dialog.getByRole('button', { name: 'Revoke token' }).click();
        await expect(page.getByText(`Revoked “${name}”.`)).toBeVisible();
        await expect(
          page.getByRole('row', { name: new RegExp(name) }),
        ).toHaveCount(0);

        const refused = await cli('deploy', fixture, site, '--json').then(
          () => null,
          (err: { code: number; stderr: string }) => err,
        );
        expect(refused?.code).toBe(1);
        expect(refused?.stderr).toContain('run wardlock login again');
        const me = await page.request.fetch(`${origin}/api/v0/me`, {
          headers: { Authorization: `Bearer ${token}`, Cookie: '' },
        });
        expect(me.status()).toBe(401);
      });
    } finally {
      rmSync(work, { recursive: true, force: true });
    }
  });
}

test('wardlock token create prints the approved token on stdout', async ({
  page,
}, info) => {
  const work = mkdtempSync(join(tmpdir(), 'wardlock-token-'));
  const name = `CLI made ${info.project.name}`;
  try {
    const child = spawn(
      wardlock,
      ['token', 'create', '--name', name, '--server', origin],
      {
        cwd: work,
        env: { ...process.env, HOME: work, XDG_CONFIG_HOME: work },
        stdio: ['ignore', 'pipe', 'pipe'],
      },
    );
    let stdout = '';
    let stderr = '';
    child.stdout.on('data', (chunk) => (stdout += String(chunk)));
    child.stderr.on('data', (chunk) => (stderr += String(chunk)));
    const exited = new Promise<number | null>((done) =>
      child.on('close', done),
    );

    await expect.poll(() => stderr).toContain('/cli-login?code=');
    const url = /https?:\S+\/cli-login\?code=\S+/.exec(stderr)![0];
    await page.goto(url);
    await page.getByRole('button', { name: 'Approve' }).click();
    await expect(
      page.getByRole('heading', { level: 1, name: 'CLI login approved' }),
    ).toBeVisible();

    expect(await exited).toBe(0);
    expect(stdout.trim()).toMatch(new RegExp(`^${tokenShape.source}$`));
    expect(stderr).not.toMatch(tokenShape);

    // Named as asked, listed like any other, and working.
    const me = await page.request.fetch(`${origin}/api/v0/me`, {
      headers: { Authorization: `Bearer ${stdout.trim()}`, Cookie: '' },
    });
    expect(me.status()).toBe(200);
    await page.goto(`${origin}/tokens`);
    await page.getByRole('button', { name: `Revoke ${name}` }).click();
    await page.getByRole('button', { name: 'Revoke token' }).click();
    await expect(page.getByText(`Revoked “${name}”.`)).toBeVisible();
  } finally {
    rmSync(work, { recursive: true, force: true });
  }
});
