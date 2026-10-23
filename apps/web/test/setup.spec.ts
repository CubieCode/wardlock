// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only
import { expect, test } from '@playwright/test';
import {
  accessOwnerFor,
  cliOwnerFor,
  firstOwnerState,
  foreignSite,
  ownerFor,
  passkeyOwnerFor,
  projects,
  schemes,
  sitesOwnerFor,
} from './mail.js';
import { createSite } from './publish.js';

const origin = process.env.WARDLOCK_ACCEPTANCE_ORIGIN;
const setupURL = process.env.WARDLOCK_ACCEPTANCE_SETUP_URL;
if (!origin || !setupURL) throw new Error('Run through pnpm browser:test');

// Runs once, before the desktop and mobile projects: the setup URL creates
// the first owner, who invites the owners each project and theme signs in
// as.
test('the setup URL creates the first owner', async ({
  page,
  browser,
}, info) => {
  await page.goto(setupURL);
  await expect(
    page.getByRole('heading', { level: 1, name: 'Create the first owner' }),
  ).toBeVisible();
  // The one-time code leaves the address bar straight away.
  expect(new URL(page.url()).search).toBe('');
  // The first owner sees the policy before their account exists.
  await expect(
    page.getByText('By continuing you agree to the acceptable use policy.'),
  ).toBeVisible();
  await expect(
    page.getByRole('link', { name: 'acceptable use policy' }),
  ).toHaveAttribute('href', '/acceptable-use');

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
  await page
    .getByRole('navigation', { name: 'Main' })
    .getByRole('button', { name: 'Account' })
    .click();
  await expect(page.getByText('first-owner@example.test')).toBeVisible();
  await page.keyboard.press('Escape');

  for (const project of projects)
    for (const scheme of schemes)
      for (const email of [
        ownerFor(project, scheme),
        passkeyOwnerFor(project, scheme),
        cliOwnerFor(project, scheme),
        sitesOwnerFor(project, scheme),
        accessOwnerFor(project, scheme),
      ]) {
        const res = await page.request.post(`${origin}/api/v0/owners`, {
          data: { email },
          headers: { Origin: origin },
        });
        expect(res.status()).toBe(202);
      }
  await createSite(page.request, foreignSite);
  await page.context().storageState({ path: firstOwnerState });

  // The URL worked once: a browser without the new owner's session is
  // refused by the server, not just redirected by the dashboard.
  const stranger = await browser.newContext();
  try {
    const other = await stranger.newPage();
    await other.goto(setupURL);
    await other
      .getByRole('textbox', { name: 'Email address' })
      .fill('second-owner@example.test');
    await other
      .getByRole('button', { name: 'Create owner and sign in' })
      .click();
    await expect(
      other.getByRole('heading', {
        level: 1,
        name: 'This setup link no longer works',
      }),
    ).toBeVisible();
    expect(await stranger.cookies(origin)).toEqual([]);
  } finally {
    await stranger.close();
  }
});
