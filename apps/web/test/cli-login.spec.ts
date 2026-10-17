// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only
import {
  expect,
  test,
  type APIRequestContext,
  type Page,
  type TestInfo,
} from '@playwright/test';
import { cliOwnerFor, mailbox, schemes } from './mail.js';

const origin = process.env.WARDLOCK_ACCEPTANCE_ORIGIN;
if (!origin) throw new Error('Run through pnpm browser:test');

async function check(page: Page, info: TestInfo, name: string) {
  const overflow = await page.evaluate(
    () =>
      document.documentElement.scrollWidth -
      document.documentElement.clientWidth,
  );
  expect(overflow).toBeLessThanOrEqual(0);
  await page.screenshot({
    path: info.outputPath(`${name}.png`),
    fullPage: true,
  });
}

interface CliLogin {
  deviceCode: string;
  userCode: string;
  verificationUrl: string;
  verificationUrlComplete: string;
  interval: number;
}

/** Starts a login the way `wardlock login` does: no cookies, no Origin. */
async function startLogin(cli: APIRequestContext): Promise<CliLogin> {
  const res = await cli.post(`${origin}/api/v0/cli-login`, {
    data: { clientName: 'wardlock acceptance' },
  });
  expect(res.status()).toBe(201);
  return (await res.json()) as CliLogin;
}

for (const scheme of schemes) {
  test(`CLI login approval (${scheme})`, async ({ page, playwright }, info) => {
    await page.emulateMedia({ colorScheme: scheme });
    const shot = (step: string) => check(page, info, `cli-${step}-${scheme}`);
    const cli = await playwright.request.newContext();
    try {
      await test.step('the owner signs in', async () => {
        const inbox = mailbox(cliOwnerFor(info.project.name, scheme));
        await page.goto(await inbox.signInLink());
        await expect(
          page.getByRole('heading', { level: 1, name: 'Overview' }),
        ).toBeVisible();
      });

      await test.step('the page the CLI opens approves the login', async () => {
        const login = await startLogin(cli);
        expect(login.verificationUrlComplete).toBe(
          `${origin}/cli-login?code=${login.userCode}`,
        );
        const pending = await cli.post(`${origin}/api/v0/cli-login/poll`, {
          data: { deviceCode: login.deviceCode },
        });
        expect(pending.status()).toBe(202);

        await page.goto(login.verificationUrlComplete);
        await expect(
          page.getByRole('heading', { level: 1, name: 'Approve CLI login' }),
        ).toBeVisible();
        await expect(
          page.getByText(login.userCode, { exact: true }),
        ).toBeVisible();
        await expect(
          page.getByRole('checkbox', { name: /Deploy/ }),
        ).toBeChecked();
        await expect(
          page.getByRole('checkbox', { name: /Manage/ }),
        ).toBeChecked();
        await shot('review');
        await page.getByRole('checkbox', { name: /Manage/ }).uncheck();
        await page.getByRole('button', { name: 'Approve' }).click();
        await expect(
          page.getByRole('heading', { level: 1, name: 'CLI login approved' }),
        ).toBeVisible();
        await shot('approved');

        // The CLI's next poll after the interval gets a deploy-only token.
        await page.waitForTimeout(login.interval * 1000);
        const done = await cli.post(`${origin}/api/v0/cli-login/poll`, {
          data: { deviceCode: login.deviceCode },
        });
        expect(done.status()).toBe(200);
        const { token, apiToken } = (await done.json()) as {
          token: string;
          apiToken: { scopes: string[] };
        };
        expect(apiToken.scopes).toEqual(['deploy']);
        const me = await cli.get(`${origin}/api/v0/me`, {
          headers: { Authorization: `Bearer ${token}` },
        });
        expect(me.status()).toBe(200);
        expect(await me.json()).toMatchObject({
          credential: 'apiToken',
          owner: { email: cliOwnerFor(info.project.name, scheme) },
        });
      });

      await test.step('a typed code, a wrong code and a denial', async () => {
        const login = await startLogin(cli);
        await page.goto(`${origin}/cli-login`);
        await expect(
          page.getByRole('heading', {
            level: 1,
            name: 'Log in to the Wardlock CLI',
          }),
        ).toBeVisible();
        const field = page.getByRole('textbox', {
          name: 'Code from your terminal',
        });
        await field.fill('nope');
        await page.getByRole('button', { name: 'Continue' }).click();
        await expect(field).toHaveAttribute('aria-invalid', 'true');
        await shot('enter-code');
        await field.fill(login.userCode.toLowerCase().replace('-', ' '));
        await page.getByRole('button', { name: 'Continue' }).click();
        await expect(
          page.getByText(login.userCode, { exact: true }),
        ).toBeVisible();
        await page.getByRole('button', { name: 'Deny' }).click();
        await expect(
          page.getByRole('heading', { level: 1, name: 'CLI login denied' }),
        ).toBeVisible();
        await shot('denied');
        const gone = await cli.post(`${origin}/api/v0/cli-login/poll`, {
          data: { deviceCode: login.deviceCode },
        });
        expect(gone.status()).toBe(410);

        // The decided code no longer works.
        await page.goto(`${origin}/cli-login?code=${login.userCode}`);
        await page.getByRole('button', { name: 'Approve' }).click();
        await expect(
          page.getByText(/That code is wrong, has expired or was already used/),
        ).toBeVisible();
        await shot('invalid-code');
      });
    } finally {
      await cli.dispose();
    }
  });
}
