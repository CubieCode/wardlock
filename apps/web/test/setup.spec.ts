// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only
import { expect, test } from '@playwright/test';
import { ownerFor, projects, schemes } from './mail.js';

const origin = process.env.WARDLOCK_ACCEPTANCE_ORIGIN;
const setupURL = process.env.WARDLOCK_ACCEPTANCE_SETUP_URL;
if (!origin || !setupURL) throw new Error('Run through pnpm browser:test');

// Runs once, before the desktop and mobile projects: the setup URL creates
// the first owner, who invites one owner per project and theme.
test('the setup URL creates the first owner', async ({ page }, info) => {
  await page.goto(setupURL);
  await expect(
    page.getByRole('heading', { level: 1, name: 'Create the first owner' }),
  ).toBeVisible();
  // The one-time code leaves the address bar straight away.
  expect(new URL(page.url()).search).toBe('');

  for (const [width, height] of [
    [1440, 900],
    [390, 844],
  ] as const)
    for (const scheme of schemes) {
      await page.setViewportSize({ width, height });
      await page.emulateMedia({ colorScheme: scheme });
      await page.screenshot({
        path: info.outputPath(`setup-${width}-${scheme}.png`),
        fullPage: true,
      });
    }

  await page.setViewportSize({ width: 1440, height: 900 });
  await page.emulateMedia({ colorScheme: 'light' });
  await page.getByRole('button', { name: 'Create owner and sign in' }).click();
  await expect(page.getByText('Enter your email address.')).toBeVisible();
  await page
    .getByRole('textbox', { name: 'Email address' })
    .fill('first-owner@example.test');
  await page.getByRole('button', { name: 'Create owner and sign in' }).click();
  await expect(
    page.getByRole('heading', { level: 1, name: 'Overview' }),
  ).toBeVisible();
  await expect(page.getByText('first-owner@example.test')).toBeVisible();

  for (const project of projects)
    for (const scheme of schemes) {
      const res = await page.request.post(`${origin}/api/v0/owners`, {
        data: { email: ownerFor(project, scheme) },
        headers: { Origin: origin },
      });
      expect(res.status()).toBe(202);
    }

  // The URL worked once.
  await page.goto(setupURL);
  await expect(page).toHaveURL(`${origin}/`);
});
