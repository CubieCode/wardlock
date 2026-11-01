// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// T7.2: an admin makes a team and invites a second owner through the dev mail
// sink; they follow the email's link through sign-in, accept, change a site
// as an editor and, once made a viewer, find the same controls gone.
import { expect, test, type Page, type TestInfo } from '@playwright/test';
import {
  firstOwnerState,
  messageWith,
  schemes,
  teamAdminState,
} from './mail.js';
import { goToPlace, isMobile } from './places.js';
import { publish } from './publish.js';
import { siteTab } from './site-page.js';

const origin = process.env.WARDLOCK_ACCEPTANCE_ORIGIN;
if (!origin) throw new Error('Run through pnpm browser:test');
const headers = { Origin: origin };

// The second owner is the first owner, whose session setup saved, in a team
// of each run's own: link redemptions and email-code sends are limited per
// IP, and the other specs spend nearly all of both.
const email = 'first-owner@example.test';

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
    animations: 'disabled',
  });
}

const switcher = (page: Page) =>
  page.getByRole('button', { name: /^Switch team/ });

for (const scheme of schemes) {
  test(`a team shares a site, by role (${scheme})`, async ({
    browser,
  }, info) => {
    const shot = (page: Page, step: string) =>
      check(page, info, `teams-${step}-${scheme}`);
    const lane = `${info.project.name}-${scheme}`;
    const teamName = `Team ${lane}`;
    const site = `team-${lane}`;
    const own = `moves-${lane}`;
    const options = {
      colorScheme: scheme,
      viewport: info.project.use.viewport!,
    };

    const adminContext = await browser.newContext({
      ...options,
      storageState: teamAdminState,
    });
    const memberContext = await browser.newContext({
      ...options,
      storageState: firstOwnerState,
    });
    const admin = await adminContext.newPage();
    const member = await memberContext.newPage();

    try {
      let teamId = '';

      await test.step('an admin creates a team from the switcher', async () => {
        await admin.goto(`${origin}/`);
        await switcher(admin).click();
        await shot(admin, 'switcher');
        await admin.getByRole('menuitem', { name: 'Create team' }).click();
        await admin.getByRole('textbox', { name: 'Team name' }).fill(teamName);
        await admin
          .getByRole('dialog')
          .getByRole('button', { name: 'Create team' })
          .click();
        await expect(
          admin.getByRole('heading', { level: 1, name: teamName }),
        ).toBeVisible();
        teamId = new URL(admin.url()).pathname.split('/').pop()!;
        await expect(admin.getByText('You are an admin')).toBeVisible();
      });

      await test.step('a site of the team shows only in the team', async () => {
        const created = await admin.request.post(`${origin}/api/v0/sites`, {
          data: { name: site, team: teamId },
          headers,
        });
        expect(created.status()).toBe(201);
        await publish(admin.request, site, 'First');
        await publish(admin.request, site, 'Second');
        await admin.goto(`${origin}/sites?team=${teamId}`);
        await expect(
          admin.getByRole('link', { name: site, exact: true }).first(),
        ).toBeVisible();
        await switcher(admin).click();
        await admin.getByRole('menuitemcheckbox', { name: 'Personal' }).click();
        await expect(admin).toHaveURL(`${origin}/sites`);
        await expect(
          admin.getByRole('link', { name: site, exact: true }),
        ).toHaveCount(0);
      });

      await test.step('the admin invites a second owner', async () => {
        await admin.goto(`${origin}/teams/${teamId}`);
        await admin.getByRole('textbox', { name: 'Email address' }).fill(email);
        await admin.getByRole('button', { name: 'Send invitation' }).click();
        await expect(admin.getByText(`Invited ${email}`)).toBeVisible();
        await shot(admin, 'settings');
      });

      await test.step('the email’s link leads through sign-in to the invitation', async () => {
        const message = await messageWith(email, teamName);
        const link = /https?:\/\/\S+\/team-invitations/.exec(message)![0];
        const { pathname } = new URL(link);
        expect(pathname).toBe('/team-invitations');
        // Signed out, the link goes through sign-in and comes back. Their
        // session is put back rather than a new one made: email-code sends
        // are limited per IP, and the other specs use them.
        const session = await memberContext.cookies();
        await memberContext.clearCookies();
        await member.goto(`${origin}${pathname}`);
        await expect(member).toHaveURL(
          `${origin}/sign-in?next=${encodeURIComponent(pathname)}`,
        );
        await memberContext.addCookies(session);
        await member.reload();
        await expect(member).toHaveURL(`${origin}${pathname}`);
        await expect(
          member.getByRole('heading', { level: 1, name: 'Team invitations' }),
        ).toBeVisible();
        await expect(
          member.getByRole('heading', { name: teamName }),
        ).toBeVisible();
        await shot(member, 'invitation');
      });

      await test.step('they accept and land in the team', async () => {
        await member
          .getByRole('button', {
            name: `Accept the invitation to ${teamName}`,
          })
          .click();
        await expect(member).toHaveURL(`${origin}/sites?team=${teamId}`);
        await expect(
          member.getByRole('link', { name: site, exact: true }).first(),
        ).toBeVisible();
      });

      await test.step('an editor changes the site', async () => {
        await member.goto(`${origin}/sites/${site}/links`);
        // A site's address joins its team to the URL however it was reached.
        await expect(member).toHaveURL(new RegExp(`team=${teamId}`));
        await member.getByRole('button', { name: 'Create link' }).click();
        await member
          .getByRole('textbox', { name: /Label/ })
          .fill('For the review');
        await member
          .getByRole('dialog')
          .getByRole('button', { name: 'Create link' })
          .click();
        await member.getByRole('button', { name: 'Done' }).click();
        await expect(member.getByText(`Created by ${email}`)).toBeVisible();
        await shot(member, 'editor-links');
      });

      await test.step('the admin makes them a viewer', async () => {
        await admin.reload();
        const role = admin.getByRole('combobox', { name: `Role of ${email}` });
        const save = admin.getByRole('button', {
          name: `Save the role of ${email}`,
        });
        // Choosing changes nothing until Save.
        await role.selectOption('admin');
        await shot(admin, 'role-save');
        await save.click();
        await expect(
          admin.getByRole('dialog', {
            name: `Make ${email} an admin of ${teamName}?`,
          }),
        ).toBeVisible();
        await shot(admin, 'role-confirm');
        await admin.getByRole('button', { name: 'Cancel' }).click();
        await role.selectOption('viewer');
        await save.click();
        await expect(save).toHaveCount(0);
        await expect(role).toHaveValue('viewer');
      });

      await test.step('the same controls are missing as a viewer', async () => {
        await member.reload();
        await expect(member.getByText('For the review')).toBeVisible();
        await expect(
          member.getByRole('button', { name: 'Create link' }),
        ).toHaveCount(0);
        await expect(
          member.getByRole('button', { name: /^Revoke/ }),
        ).toHaveCount(0);
        await expect(
          member.getByRole('button', { name: /^Rotate/ }),
        ).toHaveCount(0);
        await shot(member, 'viewer-links');

        await siteTab(member, 'Access');
        await expect(
          member.getByText('Site default', { exact: true }),
        ).toBeVisible();
        await expect(
          member.getByRole('button', { name: 'Add path policy' }),
        ).toHaveCount(0);
        await expect(member.getByRole('button', { name: /^Edit/ })).toHaveCount(
          0,
        );

        await siteTab(member, 'Deploys');
        await expect(
          member.getByRole('list', { name: 'Deploy history' }),
        ).toBeVisible();
        await expect(
          member.getByRole('button', { name: /^Roll back/ }),
        ).toHaveCount(0);

        await siteTab(member, 'Settings');
        await expect(
          member.getByRole('heading', { name: 'Group and tags' }),
        ).toBeVisible();
        await expect(
          member.getByRole('button', { name: 'Tear down site' }),
        ).toHaveCount(0);
        await expect(
          member.getByRole('heading', { name: 'Move this site' }),
        ).toHaveCount(0);
        await shot(member, 'viewer-settings');

        await member.goto(`${origin}/sites?team=${teamId}`);
        await expect(
          member.getByRole('link', { name: site, exact: true }).first(),
        ).toBeVisible();
        await expect(
          member.getByRole('checkbox', { name: `Select ${site}` }),
        ).toHaveCount(0);

        await member.goto(`${origin}/teams/${teamId}`);
        await expect(
          member.getByRole('heading', { name: 'Members' }),
        ).toBeVisible();
        await expect(
          member.getByRole('button', { name: 'Send invitation' }),
        ).toHaveCount(0);
        await expect(
          member.getByRole('button', { name: 'Delete team' }),
        ).toHaveCount(0);
        await shot(member, 'viewer-settings-page');
      });

      await test.step('a page of the owner’s own is personal', async () => {
        await admin.goto(`${origin}/sites?team=${teamId}`);
        await goToPlace(admin, info, 'API tokens');
        await expect(admin).toHaveURL(`${origin}/tokens`);
        await expect(
          admin.getByRole('button', { name: 'Switch team: Personal' }),
        ).toBeVisible();
        await shot(admin, 'personal-page');
      });

      await test.step('the bars’ popovers open over everything', async () => {
        const items = async () => {
          await switcher(admin).click();
          for (const item of await admin
            .getByRole('menu', { name: 'Teams' })
            .getByRole('menuitemcheckbox')
            .all())
            await item.click({ trial: true });
          await shot(
            admin,
            `switcher-${new URL(admin.url()).searchParams.get('view') ?? 'list'}`,
          );
          await admin.keyboard.press('Escape');
        };
        await admin.goto(`${origin}/sites?team=${teamId}`);
        await expect(
          admin.getByRole('link', { name: site, exact: true }).first(),
        ).toBeVisible();
        await items();
        await admin.goto(`${origin}/sites?view=map&team=${teamId}`);
        await expect(
          admin.getByRole('heading', { level: 1, name: /map/i }),
        ).toBeVisible();
        await items();

        // Narrow enough that the bulk bar reaches under the account menu.
        const size = admin.viewportSize()!;
        if (!isMobile(info))
          await admin.setViewportSize({ width: 800, height: 600 });
        await admin.goto(`${origin}/sites?team=${teamId}`);
        if (isMobile(info))
          await admin.getByRole('button', { name: 'Select' }).click();
        await admin.getByRole('checkbox', { name: `Select ${site}` }).check();
        await expect(
          admin.getByRole('toolbar', { name: 'Bulk actions' }),
        ).toBeVisible();
        await admin
          .getByRole('navigation', { name: 'Main' })
          .getByRole('button', { name: 'Account' })
          .click();
        await admin
          .getByRole('button', { name: 'Sign out' })
          .click({ trial: true });
        await shot(admin, 'account-over-bulk');
        await admin.keyboard.press('Escape');
        await admin.setViewportSize(size);
      });

      await test.step('a site moves to the team and back', async () => {
        const created = await admin.request.post(`${origin}/api/v0/sites`, {
          data: { name: own },
          headers,
        });
        expect(created.status()).toBe(201);
        await admin.goto(`${origin}/sites/${own}/settings`);
        await admin.getByRole('button', { name: 'Move site' }).click();
        await admin
          .getByRole('dialog')
          .getByRole('combobox', { name: 'Move to' })
          .selectOption({ label: teamName });
        await shot(admin, 'move');
        await admin
          .getByRole('dialog')
          .getByRole('button', { name: 'Move site' })
          .click();
        await expect(admin).toHaveURL(new RegExp(`team=${teamId}`));
        await expect(admin.getByText(`Moved ${own} to`)).toBeVisible();
        await expect(admin.getByRole('link', { name: teamName })).toBeVisible();

        await admin.getByRole('button', { name: 'Move site' }).click();
        await admin
          .getByRole('dialog')
          .getByRole('button', { name: 'Move site' })
          .click();
        await expect(admin.getByText(`Moved ${own} to`)).toBeVisible();
        await expect(admin).not.toHaveURL(/team=/);
      });

      await test.step('a team token is shown once', async () => {
        await admin.goto(`${origin}/teams/${teamId}`);
        await admin.getByRole('button', { name: 'Create team token' }).click();
        await admin
          .getByRole('dialog')
          .getByRole('textbox', { name: 'Name' })
          .fill('CI');
        await admin
          .getByRole('dialog')
          .getByRole('button', { name: 'Create token' })
          .click();
        await expect(admin.getByText('Copy your new team token')).toBeVisible();
        await admin.getByRole('button', { name: 'Done' }).click();
        await expect(
          admin.getByRole('table', { name: /Team tokens/ }),
        ).toBeVisible();
      });

      await test.step('a team with sites is not deleted, and members can leave', async () => {
        await admin.getByRole('button', { name: 'Delete team' }).click();
        await admin
          .getByRole('textbox', { name: `Type ${teamName} to confirm` })
          .fill(teamName);
        await admin
          .getByRole('dialog')
          .getByRole('button', { name: 'Delete team' })
          .click();
        await expect(admin.getByText(/still has sites/)).toBeVisible();

        await member.getByRole('button', { name: 'Leave team' }).click();
        await member
          .getByRole('dialog')
          .getByRole('button', { name: 'Leave team' })
          .click();
        await expect(member).toHaveURL(`${origin}/sites`);
        await member.goto(`${origin}/sites/${site}`);
        await expect(
          member.getByRole('heading', { level: 1, name: 'Site not found' }),
        ).toBeVisible();
      });
    } finally {
      await adminContext.close();
      await memberContext.close();
    }
  });
}
