// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Requests by country (V5.14), against a real server whose country lookup
// reads a synthetic database placing 127.0.0.0/24 (every request here) in
// NZ, with the server drawn in NZ (scripts/browser-acceptance.mjs). A site
// gets public, refused and share-link requests; the analytics page shows
// "Where requests come from" with its map, list, credit and table, the
// bubbles answer the keyboard, and both access logs show a Country column
// and the credit.
// A request from 127.0.1.1, which the database places in Iceland at the
// map's left edge, checks that an edge bubble's tooltip stays on the page.
// The off and Unknown states are covered by the component tests, since this
// server's lookup is on. No token may be on the page or in a screenshot.
import { request as httpRequest } from 'node:http';
import { expect, test, type Page, type TestInfo } from '@playwright/test';
import { firstOwnerState, schemes } from './mail.js';
import { createSite, protect, publish } from './publish.js';
import { onPhone, visitLog, visitRows } from './site-page.js';

const origin = process.env.WARDLOCK_ACCEPTANCE_ORIGIN;
if (!origin) throw new Error('Run through pnpm browser:test');
test.use({ storageState: firstOwnerState });

const headers = { Origin: origin };
const tokenShape = /wl_[a-z2-7]{10}_[A-Za-z0-9_-]{43}/;

/** No sideways scroll, no token, then a full-page screenshot. */
async function check(page: Page, info: TestInfo, name: string) {
  const overflow = await page.evaluate(
    () =>
      document.documentElement.scrollWidth -
      document.documentElement.clientWidth,
  );
  expect(overflow).toBeLessThanOrEqual(0);
  expect(await page.content()).not.toMatch(tokenShape);
  await page.screenshot({
    path: info.outputPath(`${name}.png`),
    fullPage: true,
  });
}

/** GETs a URL connecting from 127.0.1.1 (Iceland in the test database). */
function getFromIceland(url: string) {
  const { hostname, port, pathname } = new URL(url);
  return new Promise<number>((resolve, reject) => {
    httpRequest(
      {
        host: '127.0.0.1',
        port,
        path: pathname,
        headers: { Host: `${hostname}:${port}` },
        localAddress: '127.0.1.1',
      },
      (response) => {
        response.resume();
        resolve(response.statusCode ?? 0);
      },
    )
      .on('error', reject)
      .end();
  });
}

for (const scheme of schemes) {
  test(`countries: card, table and log column (${scheme})`, async ({
    page,
    request,
  }, info) => {
    await page.emulateMedia({ colorScheme: scheme });
    const shot = (step: string) =>
      check(page, info, `countries-${step}-${scheme}`);
    const site = `geo-${info.project.name}-${scheme}`;
    const api = page.request;

    await createSite(api, site);
    await publish(api, site, 'Field notes');
    const read = await api.get(`${origin}/api/v0/sites/${site}`);
    const { url: siteUrl } = (await read.json()) as { url: string };
    for (let i = 0; i < 2; i++)
      expect((await request.get(siteUrl)).status()).toBe(200);
    await protect(api, site);
    expect((await request.get(siteUrl, { maxRedirects: 0 })).status()).toBe(
      401,
    );
    const created = await api.post(`${origin}/api/v0/sites/${site}/links`, {
      data: { label: 'Reviewer' },
      headers,
    });
    expect(created.status()).toBe(201);
    const {
      url: linkUrl,
      shareLink: { publicId },
    } = (await created.json()) as {
      url: string;
      shareLink: { publicId: string };
    };
    expect((await request.get(linkUrl, { maxRedirects: 0 })).status()).toBe(
      200,
    );

    const geo = page.getByRole('region', { name: 'Where requests come from' });

    await test.step('the card counts requests by country', async () => {
      await page.goto(`${origin}/analytics?site=${site}`);
      // The rollup is written with the access log, about once a second.
      const list = geo.getByRole('list', { name: 'Requests by country' });
      await expect(async () => {
        await page.reload();
        await expect(list.getByRole('listitem')).toHaveText(
          [/New Zealand\s*4\s*100%/],
          { timeout: 1000 },
        );
      }).toPass();
      await expect(
        geo.getByRole('img', { name: 'Your server, in New Zealand' }),
      ).toBeVisible();
      await expect(geo.getByText('Your server')).toBeVisible();
      const credit = geo.getByRole('link', {
        name: 'https://example.test/countries',
      });
      await expect(credit).toHaveAttribute(
        'href',
        'https://example.test/countries',
      );
      await expect(page.getByText(/visits?\b/i)).toHaveCount(0);
      await shot('card');
    });

    await test.step('bubbles answer the keyboard', async () => {
      const bubble = geo.getByRole('img', {
        name: 'New Zealand: 4 requests · 100%',
      });
      await bubble.focus();
      const tip = geo.locator('.country-map').getByRole('tooltip');
      await expect(tip).toBeVisible();
      await expect(tip).toContainText('New Zealand');
      await expect(tip).toContainText('4 requests · 100%');
      await page.keyboard.press('Escape');
      await expect(tip).toBeHidden();
    });

    await test.step('the card opens as a table', async () => {
      await geo.getByText('View as table').click();
      await expect(
        geo
          .getByRole('table', { name: 'Requests by country' })
          .getByRole('row'),
      ).toHaveText([
        /Country\s*Allowed\s*Refused\s*Share/,
        /New Zealand\s*3\s*1\s*100%/,
      ]);
      await shot('table');
    });

    await test.step('the access logs have a Country column and the credit', async () => {
      await page.goto(`${origin}/sites/${site}/access-log`);
      const phone = onPhone(info);
      const log = `Requests for ${site}`;
      // Phones have no columns: the country follows the IP address.
      if (!phone)
        await expect(
          visitLog(page, log, phone).getByRole('columnheader', {
            name: 'Country',
          }),
        ).toHaveCount(1);
      await expect(visitRows(page, log, phone).first()).toContainText(
        'New Zealand',
      );
      await expect(
        page.getByRole('link', { name: 'https://example.test/countries' }),
      ).toHaveAttribute('href', 'https://example.test/countries');
      await shot('site-log');

      await page.goto(`${origin}/sites/${site}/links/${publicId}`);
      const linkLog = `Requests through link ${publicId}`;
      if (!phone)
        await expect(
          visitLog(page, linkLog, phone).getByRole('columnheader', {
            name: 'Country',
          }),
        ).toHaveCount(1);
      await expect(visitRows(page, linkLog, phone).first()).toContainText(
        'New Zealand',
      );
      await expect(
        page.getByRole('link', { name: 'https://example.test/countries' }),
      ).toHaveAttribute('href', 'https://example.test/countries');
      await shot('link-log');
    });
  });

  test(`countries: an edge bubble's tooltip stays on the page (${scheme})`, async ({
    page,
  }, info) => {
    await page.emulateMedia({ colorScheme: scheme });
    const site = `edge-${info.project.name}-${scheme}`;
    const api = page.request;
    await createSite(api, site);
    await publish(api, site, 'Field notes');
    const read = await api.get(`${origin}/api/v0/sites/${site}`);
    const { url: siteUrl } = (await read.json()) as { url: string };
    expect(await getFromIceland(siteUrl)).toBe(200);

    await page.goto(`${origin}/analytics?site=${site}`);
    const geo = page.getByRole('region', { name: 'Where requests come from' });
    const bubble = geo.getByRole('img', { name: /^Iceland: 1 request/ });
    await expect(async () => {
      await page.reload();
      await expect(bubble).toBeVisible({ timeout: 1000 });
    }).toPass();
    await bubble.focus();
    const map = geo.locator('.country-map');
    const tip = map.getByRole('tooltip');
    await expect(tip).toContainText('Iceland');
    const inner = (await tip.boundingBox())!;
    const outer = (await map.boundingBox())!;
    expect(inner.x).toBeGreaterThanOrEqual(outer.x);
    expect(inner.x + inner.width).toBeLessThanOrEqual(outer.x + outer.width);
    expect(inner.y).toBeGreaterThanOrEqual(outer.y);
    await check(page, info, `countries-edge-tip-${scheme}`);
  });
}
