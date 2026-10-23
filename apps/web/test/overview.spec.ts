// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only
import {
  expect,
  test,
  type APIRequestContext,
  type Page,
  type TestInfo,
} from '@playwright/test';
import { firstOwnerState, schemes } from './mail.js';
import { createSite, protect, publish } from './publish.js';

const origin = process.env.WARDLOCK_ACCEPTANCE_ORIGIN;
if (!origin) throw new Error('Run through pnpm browser:test');
const headers = { Origin: origin };

// The first owner's saved session: email-code sends are limited per IP.
// Other specs add sites to this owner too, so each step looks for this
// run's own sites, group and link rather than exact counts. The empty
// account is checked in acceptance.spec.ts, by a new owner.
test.use({ storageState: firstOwnerState });

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

async function api<T>(
  request: APIRequestContext,
  method: 'post' | 'put',
  path: string,
  data?: unknown,
): Promise<T> {
  const res = await request[method](`${origin}/api/v0${path}`, {
    data,
    headers,
  });
  expect(res.ok()).toBe(true);
  const text = await res.text();
  return (text ? JSON.parse(text) : undefined) as T;
}

for (const scheme of schemes) {
  test(`overview: attention, pinned, groups and deploys (${scheme})`, async ({
    page,
  }, info) => {
    await page.emulateMedia({ colorScheme: scheme });
    const shot = (step: string) =>
      check(page, info, `overview-${step}-${scheme}`);
    const run = `${info.project.name}-${scheme}`;
    const site = `ov-${run}`;
    const label = `Overview review ${run}`;
    const groupName = `Overview ${run}`;

    const request = page.request;
    await createSite(request, site);
    await protect(request, site);
    const group = await api<{ id: string }>(request, 'post', '/groups', {
      name: groupName,
    });
    await api(request, 'put', `/sites/${site}/organisation`, {
      groupId: group.id,
      tagIds: [],
    });
    await api(request, 'put', `/sites/${site}/pin`);
    // Lives just over an hour, so it counts as expiring soon and sorts
    // before any other spec's links.
    const { shareLink } = await api<{ shareLink: { id: string } }>(
      request,
      'post',
      `/sites/${site}/links`,
      { label, maxUses: 3, expiresAt: Date.now() + 61 * 60_000 },
    );
    await publish(request, site, `Overview launch ${run}`);

    await test.step('the greeting names the server version', async () => {
      await page.goto(`${origin}/`);
      await expect(
        page.getByRole('heading', { level: 1, name: 'Overview' }),
      ).toBeVisible();
      await expect(
        page.getByText(/You have \d+ sites? in \d+ groups?\./),
      ).toBeVisible();
      await expect(page.getByText(/This server runs Wardlock/)).toBeVisible();
    });

    const attention = page.getByRole('region', { name: 'Needs attention' });
    await test.step('needs attention lists the expiring link and the views', async () => {
      const row = attention
        .getByRole('list', { name: 'Links expiring soon' })
        .getByRole('listitem')
        .filter({ hasText: label });
      await expect(row).toContainText(`${label} on ${site}`);
      await expect(row).toContainText('3 uses left');
      await expect(
        attention.getByRole('link', {
          name: 'Review sites with links expiring soon',
        }),
      ).toHaveAttribute('href', '/sites?v=expiring');
      await shot('populated');
      await row
        .getByRole('link', { name: `View link ${label} on ${site}` })
        .click();
      await expect(page).toHaveURL(
        `${origin}/sites/${site}/links/${shareLink.id}`,
      );
      await page.goBack();
    });

    await test.step('pinned shows access, requests and a sparkline', async () => {
      const pinned = page
        .getByRole('region', { name: 'Pinned' })
        .getByRole('listitem')
        .filter({ has: page.getByRole('link', { name: site, exact: true }) });
      await expect(pinned).toContainText('Protected');
      await expect(pinned).toContainText(/\d+ requests? in 7 days/);
      await expect(pinned.locator('svg.wl-sparkline')).toHaveCount(1);
    });

    await test.step('groups link to their filtered list', async () => {
      const row = page
        .getByRole('region', { name: 'Groups' })
        .getByRole('listitem')
        .filter({ hasText: groupName });
      await expect(row).toContainText('1 site · 0 requests in 7 days · 1 link');
      await expect(row.getByRole('link', { name: groupName })).toHaveAttribute(
        'href',
        `/sites?g=${group.id}`,
      );
    });

    await test.step('recent deploys show the live deploy', async () => {
      const deploys = page.getByRole('region', { name: 'Recent deploys' });
      await expect(
        deploys.getByText('Each site’s live deploy, newest first.'),
      ).toBeVisible();
      // Other specs deploy to this owner at the same time and can push this
      // run's deploy out of the newest few; deploy again until it is shown.
      await expect(async () => {
        await publish(request, site, `Overview update ${run}`);
        await page.reload();
        await expect(
          deploys.getByRole('listitem').filter({ hasText: site }),
        ).toContainText(`Overview update ${run}`, { timeout: 2000 });
      }).toPass({ timeout: 30_000 });
    });

    await test.step('the rail marks Overview as current', async () => {
      await expect(
        page
          .getByRole('navigation', { name: 'Main' })
          .getByRole('link', { name: 'Overview' }),
      ).toHaveAttribute('aria-current', 'page');
    });
  });
}
