// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// The site page's header and tabs (V5.10), against a real server: pin the
// site, create and remove a tag from + Tag, file it in a group from
// Settings, and look at every tab. Each tab is screenshotted at the
// project's width in this colour scheme, with no sideways scroll.
import { expect, test, type Page, type TestInfo } from '@playwright/test';
import { firstOwnerState, schemes } from './mail.js';
import { createSite, protect, publish } from './publish.js';
import { siteTab } from './site-page.js';

const origin = process.env.WARDLOCK_ACCEPTANCE_ORIGIN;
if (!origin) throw new Error('Run through pnpm browser:test');
// Signed in as the first owner, whose session setup saved: email-code sends
// are limited per IP, and the other specs use them.
test.use({ storageState: firstOwnerState });
const headers = { Origin: origin };

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

for (const scheme of schemes) {
  test(`site page: header, pin, tags, group and every tab (${scheme})`, async ({
    page,
  }, info) => {
    await page.emulateMedia({ colorScheme: scheme });
    const shot = (step: string) =>
      check(page, info, `site-page-${step}-${scheme}`);
    const site = `tabs-${info.project.name}-${scheme}`;
    const tag = `t-${info.project.name}-${scheme}`;
    const groupName = `Group ${info.project.name} ${scheme}`;
    const chips = page.getByRole('group', { name: 'Group and tags' });
    const read = async () =>
      (await (
        await page.request.get(`${origin}/api/v0/sites/${site}`)
      ).json()) as { pinned: boolean; tagIds: string[]; groupId?: string };

    await test.step('the header shows access, address and when it went live', async () => {
      await createSite(page.request, site);
      await publish(page.request, site, 'Tabbed notes');
      await protect(page.request, site);
      const group = await page.request.post(`${origin}/api/v0/groups`, {
        data: { name: groupName },
        headers,
      });
      expect(group.status()).toBe(201);
      await page.goto(`${origin}/sites/${site}`);
      await expect(
        page.getByRole('heading', { level: 1, name: site }),
      ).toBeVisible();
      await expect(page.locator('.site-header-access')).toHaveText('Protected');
      await expect(
        page.getByRole('textbox', { name: 'Site address' }),
      ).toHaveValue(new RegExp(`^http://${site}\\.wardlock\\.localhost:`));
      await expect(page.getByText(/^Live since/).first()).toBeVisible();
      // Requests over the last 7 days, once the count is read.
      await expect(page.locator('.site-header-meta')).toContainText(
        /^\d[\d,]* requests? in 7 days/,
      );
      await expect(page.locator('.site-header-meta')).not.toContainText(
        /visit/i,
      );
      await expect(chips.getByText('No group')).toBeVisible();
      await expect(
        page
          .getByRole('navigation', { name: 'Site sections' })
          .getByRole('link', { name: 'Overview' }),
      ).toHaveAttribute('aria-current', 'page');
      await shot('overview');
    });

    await test.step('pinning is a toggle the server keeps', async () => {
      const pin = page.getByRole('button', { name: 'Pin site' });
      await expect(pin).toHaveAttribute('aria-pressed', 'false');
      await pin.click();
      await expect(pin).toHaveAttribute('aria-pressed', 'true');
      expect((await read()).pinned).toBe(true);
    });

    await test.step('+ Tag creates a tag and gives it to the site', async () => {
      await chips.getByRole('button', { name: 'Tag', exact: true }).click();
      const filter = page.getByRole('searchbox', {
        name: 'Find or create a tag',
      });
      await expect(filter).toBeFocused();
      await filter.fill(tag);
      await page.getByRole('menuitem', { name: `Create tag “${tag}”` }).click();
      await expect(
        chips.getByRole('button', { name: `Remove ${tag}` }),
      ).toBeVisible();
      await page.keyboard.press('Escape');
      expect((await read()).tagIds).toHaveLength(1);
      await shot('tagged');
      if (info.project.name === 'mobile') {
        // On a touch screen the small remove button takes taps across a
        // 44px square centred on it.
        const remove = chips.getByRole('button', { name: `Remove ${tag}` });
        const hits = await remove.evaluate((button) => {
          const box = button.getBoundingClientRect();
          const x = box.left + box.width / 2;
          const y = box.top + box.height / 2;
          return {
            coarse: matchMedia('(pointer: coarse)').matches,
            drawn: Math.max(box.width, box.height),
            hit: [
              [x - 21, y],
              [x + 21, y],
              [x, y - 21],
              [x, y + 21],
            ].every(
              ([px, py]) =>
                document.elementFromPoint(px!, py!)?.closest('button') ===
                button,
            ),
          };
        });
        expect(hits).toEqual({ coarse: true, drawn: hits.drawn, hit: true });
        expect(hits.drawn).toBeLessThan(44);
      }
    });

    await test.step('every tab has its own address and keeps the header', async () => {
      for (const [name, path] of [
        ['Links', 'links'],
        ['Access', 'access'],
        ['Domains', 'domains'],
        ['Deploys', 'deploys'],
        ['Access log', 'access-log'],
      ] as const) {
        await siteTab(page, name);
        await expect(page).toHaveURL(`${origin}/sites/${site}/${path}`);
        await expect(page).toHaveTitle(`${name} · ${site} · Wardlock`);
        await expect(
          page.getByRole('heading', { level: 1, name: site }),
        ).toBeVisible();
        await shot(path);
      }
      // A tab's address opens straight to it.
      await page.reload();
      await expect(
        page.getByRole('heading', { name: 'No visits yet' }),
      ).toBeVisible();
    });

    await test.step('Settings files the site in a group and removes the tag', async () => {
      await siteTab(page, 'Settings');
      await page
        .getByRole('combobox', { name: 'Group' })
        .selectOption({ label: groupName });
      await expect(chips).toContainText(`Group: ${groupName}`);
      await chips.getByRole('button', { name: `Remove ${tag}` }).click();
      await expect(
        chips.getByRole('button', { name: `Remove ${tag}` }),
      ).toHaveCount(0);
      const now = await read();
      expect(now.groupId).toBeTruthy();
      expect(now.tagIds).toEqual([]);
      await shot('settings');
    });
  });
}
