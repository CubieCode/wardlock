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

// The first owner's saved session: email-code sends are limited per IP. Each
// project and theme files its own 60 synthetic sites under its own prefix,
// and the list is searched by that prefix, so runs never see each other.
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
    animations: 'disabled',
  });
}

async function post<T>(request: APIRequestContext, path: string, data: object) {
  const res = await request.post(`${origin}${path}`, { data, headers });
  expect(res.ok(), `${path}: ${res.status()}`).toBe(true);
  return (await res.json()) as T;
}

interface Named {
  id: string;
  name: string;
}

async function sitesWith(
  request: APIRequestContext,
  query: Record<string, string>,
) {
  const res = await request.get(`${origin}/api/v0/site-summaries`, {
    params: { limit: 100, ...query },
  });
  expect(res.status()).toBe(200);
  const { items } = (await res.json()) as {
    items: { name: string; tagIds: string[] }[];
  };
  return items;
}

for (const scheme of schemes) {
  test(`sites list: 60 sites, filters, bulk tagging and undo (${scheme})`, async ({
    page,
  }, info) => {
    test.setTimeout(120_000);
    await page.emulateMedia({ colorScheme: scheme });
    const shot = (step: string) =>
      check(page, info, `sites-list-${step}-${scheme}`);
    const mobile = info.project.name === 'mobile';
    const cap = mobile ? 3 : 5;
    const prefix = `ls-${info.project.name}-${scheme}`;
    const name = (i: number) => `${prefix}-${String(i).padStart(2, '0')}`;
    const names = Array.from({ length: 60 }, (_, i) => name(i));
    const label = `${info.project.name} ${scheme}`;

    let alpha!: Named, beta!: Named, gamma!: Named, red!: Named, blue!: Named;
    await test.step('60 synthetic sites in three groups with two tags', async () => {
      const { request } = page;
      const group = (title: string) =>
        post<Named>(request, '/api/v0/groups', { name: `${title} ${label}` });
      const tag = (colour: string) =>
        post<Named>(request, '/api/v0/tags', { name: `${prefix}-${colour}` });
      alpha = await group('Alpha');
      beta = await group('Beta');
      gamma = await group('Gamma');
      red = await tag('red');
      blue = await tag('blue');
      for (let i = 0; i < names.length; i += 10)
        await Promise.all(
          names.slice(i, i + 10).map((site) => createSite(request, site)),
        );
      await Promise.all(
        names
          .filter((_, i) => i % 3 === 0)
          .map((site) => protect(request, site)),
      );
      for (const i of [0, 1, 20, 41])
        await publish(request, name(i), `Release ${i}`);
      // Share links on site 0: one that lasts, one expiring within 48 hours.
      for (const expiresAt of [undefined, Date.now() + 24 * 3600_000])
        await post(request, `/api/v0/sites/${name(0)}/links`, {
          label: 'Synthetic link',
          ...(expiresAt ? { expiresAt } : {}),
        });
      const organise = (sites: string[], change: object) =>
        post(request, '/api/v0/organise', { sites, ...change });
      await organise(names.slice(0, 20), { groupId: alpha.id });
      await organise(names.slice(20, 40), { groupId: beta.id });
      await organise(names.slice(40, 50), { groupId: gamma.id });
      await organise(names.slice(0, 10), { addTagIds: [red.id] });
      await organise(names.slice(5, 9), { addTagIds: [blue.id] });
      await organise([name(1), name(2)], { pinned: true });
    });

    await test.step('sections show five rows (three on mobile) and Show all', async () => {
      await page.goto(`${origin}/sites?q=${prefix}&sort=name`);
      await expect(
        page.getByRole('heading', { level: 1, name: 'Sites' }),
      ).toBeVisible();
      const sections = page.getByRole('region');
      await expect(
        page.getByRole('heading', { level: 2 }).filter({ hasText: label }),
      ).toHaveText([`Alpha ${label}`, `Beta ${label}`, `Gamma ${label}`]);
      const section = page.getByRole('region', { name: `Alpha ${label}` });
      const rows = section.getByRole('list', {
        name: `Sites in Alpha ${label}`,
      });
      await expect(rows.getByRole('listitem')).toHaveCount(cap);
      await expect(rows.getByRole('listitem').first()).toContainText(name(0));
      await expect(rows.getByRole('listitem').first()).toContainText(
        'Protected',
      );
      // On mobile a row is compact: no deploy message, and "1 expiring".
      if (mobile) {
        await expect(rows.getByRole('listitem').first()).toContainText(
          '2 links1 expiring within 48 hours',
        );
        await expect(page.getByText('By group · name A to Z')).toBeVisible();
      } else {
        await expect(rows.getByRole('listitem').first()).toContainText(
          'Release 0',
        );
        await expect(rows.getByRole('listitem').first()).toContainText(
          '2 linksSoon, 1 expiring within 48 hours',
        );
      }
      await expect(sections.filter({ hasText: 'No group' })).toHaveCount(1);
      await shot('list');
      if (mobile) {
        await page.getByRole('button', { name: 'View', exact: true }).click();
        const sheet = page.getByRole('dialog', { name: 'View' });
        await sheet.getByRole('radio', { name: 'Access' }).check();
        await expect(page).toHaveURL(/by=access/);
        await shot('view-sheet');
        await sheet.getByRole('radio', { name: 'Group' }).check();
        await expect(page).not.toHaveURL(/by=access/);
        await sheet.getByRole('button', { name: 'Done' }).click();
        await expect(sheet).toBeHidden();
      } else {
        await page.getByRole('radio', { name: 'Compact' }).check();
        await expect(page).toHaveURL(/d=compact/);
        await shot('compact');
        await page.getByRole('radio', { name: 'Comfortable' }).check();
        await expect(page).not.toHaveURL(/d=compact/);
      }
      await section
        .getByRole('button', { name: `Show all 20 in Alpha ${label}` })
        .click();
      await expect(rows.getByRole('listitem')).toHaveCount(20);
      // Collapsing hides the rows and says so; on mobile sections never
      // collapse.
      if (!mobile) {
        const toggle = section.getByRole('button', {
          name: `Collapse Alpha ${label}`,
        });
        await toggle.click();
        await expect(rows).toHaveCount(0);
        await expect(
          section.getByRole('button', { name: `Expand Alpha ${label}` }),
        ).toHaveAttribute('aria-expanded', 'false');
        await section
          .getByRole('button', { name: `Expand Alpha ${label}` })
          .click();
        await expect(rows.getByRole('listitem')).toHaveCount(20);
      }
    });

    await test.step('the navigator filters by group, with a chip and Clear all', async () => {
      if (mobile) {
        await page.getByRole('button', { name: 'Browse sites' }).click();
        const drawer = page.getByRole('dialog', { name: 'Browse sites' });
        await expect(
          drawer.getByRole('link', { name: /^All sites/ }),
        ).toBeVisible();
        await shot('drawer');
        await drawer
          .getByRole('link', { name: new RegExp(`^Gamma ${label} 10$`) })
          .click();
        await expect(drawer).toBeHidden();
      } else {
        await page
          .getByRole('complementary', { name: 'Sites navigator' })
          .getByRole('link', { name: new RegExp(`^Gamma ${label} 10$`) })
          .click();
      }
      await expect(
        page.getByRole('heading', { level: 1, name: `Gamma ${label}` }),
      ).toBeVisible();
      expect(new URL(page.url()).searchParams.get('g')).toBe(gamma.id);
      const filters = page.getByRole('group', { name: 'Filters' });
      await expect(filters).toContainText(`Group: Gamma ${label}`);
      // One section, so every row shows.
      await expect(
        page
          .getByRole('list', { name: `Sites in Gamma ${label}` })
          .getByRole('listitem'),
      ).toHaveCount(10);
      await filters.getByRole('button', { name: 'Clear all' }).click();
      // The navigator kept the sort the owner chose.
      await expect(page).toHaveURL(`${origin}/sites?sort=name`);
      await expect(page.getByRole('group', { name: 'Filters' })).toHaveCount(0);
    });

    await test.step('search tokens narrow the list; / focuses the search', async () => {
      const search = page.getByRole('searchbox', { name: 'Search sites' });
      await page.getByRole('heading', { level: 1 }).click();
      await page.keyboard.press('/');
      await expect(search).toBeFocused();
      await search.fill(`${prefix} tag:${prefix}-red is:protected`);
      await expect(page).toHaveURL(/q=/);
      const filters = page.getByRole('group', { name: 'Filters' });
      await expect(filters).toContainText(`Tag: ${prefix}-red`);
      await expect(filters).toContainText('Protected');
      // Sites 0, 3, 6 and 9 are protected and tagged red.
      await expect(
        page
          .getByRole('main')
          .getByRole('link', { name: new RegExp(`^${prefix}-\\d\\d$`) }),
      ).toHaveText([name(0), name(3), name(6), name(9)]);
      await shot('tokens');
      await filters
        .getByRole('button', { name: 'Remove filter Protected' })
        .click();
      await expect(search).toHaveValue(`${prefix} tag:${prefix}-red`);
      await search.fill(`${prefix}-zz-nothing`);
      await expect(
        page.getByRole('heading', { level: 2, name: 'No sites match' }),
      ).toBeVisible();
      await shot('no-match');
    });

    await test.step('bulk tagging Gamma, then Undo', async () => {
      await page.goto(`${origin}/sites?g=${gamma.id}&sort=name`);
      const section = page.getByRole('region', { name: `Gamma ${label}` });
      // On mobile select boxes show only after Select.
      if (mobile) {
        await expect(
          section.getByRole('checkbox', {
            name: `Select all in Gamma ${label}`,
          }),
        ).toHaveCount(0);
        await page.getByRole('button', { name: 'Select', exact: true }).click();
        await expect(
          page.getByRole('toolbar', { name: 'Selection' }),
        ).toContainText('Select sites');
      }
      await section
        .getByRole('checkbox', { name: `Select all in Gamma ${label}` })
        .check();
      const bar = page.getByRole('toolbar', { name: 'Bulk actions' });
      await expect(bar).toContainText('10 selected');
      await bar.getByRole('button', { name: 'Tags' }).click();
      const picker = page.getByRole('dialog', { name: 'Tags for 10 sites' });
      await expect(
        picker.getByRole('menuitemcheckbox', { name: `${prefix}-blue` }),
      ).toHaveAttribute('aria-checked', 'false');
      await shot('bulk-tags');
      await picker
        .getByRole('menuitemcheckbox', { name: `${prefix}-blue` })
        .click();
      await expect(
        page.getByRole('status').getByText(`Added ${prefix}-blue to 10 sites`),
      ).toBeVisible();
      await expect(
        picker.getByRole('menuitemcheckbox', { name: `${prefix}-blue` }),
      ).toHaveAttribute('aria-checked', 'true');
      await expect(
        section.getByRole('listitem').first().getByText(`${prefix}-blue`),
      ).toBeVisible();
      await shot('bulk-toast');
      expect(
        (await sitesWith(page.request, { tag: blue.id, q: prefix })).length,
      ).toBe(14);
      await page.keyboard.press('Escape');
      await page.getByRole('button', { name: 'Undo' }).click();
      await expect(page.getByRole('status').getByText('Undone.')).toBeVisible();
      await expect(
        section.getByRole('listitem').first().getByText(`${prefix}-blue`),
      ).toHaveCount(0);
      expect(
        (await sitesWith(page.request, { tag: blue.id, q: prefix })).length,
      ).toBe(4);
    });

    await test.step('a new tag and group from the pickers, and the pin', async () => {
      const bar = page.getByRole('toolbar', { name: 'Bulk actions' });
      await bar.getByRole('button', { name: 'Move to group' }).click();
      const picker = page.getByRole('dialog', {
        name: 'Move 10 sites to a group',
      });
      await picker
        .getByRole('searchbox', { name: 'Find or create a group' })
        .fill(`Delta ${label}`);
      await picker
        .getByRole('menuitem', { name: `Create group “Delta ${label}”` })
        .click();
      await expect(
        page.getByRole('status').getByText(`Moved 10 sites to Delta ${label}`),
      ).toBeVisible();
      // The list was filtered to Gamma, which is now empty.
      await expect(
        page.getByRole('heading', { level: 2, name: 'No sites match' }),
      ).toBeVisible();
      await page.getByRole('button', { name: 'Undo' }).click();
      await expect(
        page
          .getByRole('list', { name: `Sites in Gamma ${label}` })
          .getByRole('listitem'),
      ).toHaveCount(10);

      const row = page
        .getByRole('listitem')
        .filter({
          has: page.getByRole('link', { name: name(40), exact: true }),
        })
        .last();
      const more = row.getByRole('button', { name: `Actions for ${name(40)}` });
      if (mobile) {
        // Cancel leaves choosing; the row's ⋯ menu pins on mobile.
        await page.getByRole('button', { name: 'Cancel' }).click();
        await more.click();
        await page.getByRole('menuitem', { name: 'Pin', exact: true }).click();
        await expect(row.getByRole('img', { name: 'Pinned' })).toBeVisible();
        await expect(page.getByRole('status')).toContainText(
          'Control Z undoes it.',
        );
        await shot('undo-toast');
        await page.keyboard.press('Control+z');
        await expect(
          page.getByRole('status').getByText('Undone.'),
        ).toBeVisible();
        await expect(row.getByRole('img', { name: 'Pinned' })).toHaveCount(0);
        await expect(more).toBeFocused();
      } else {
        await row.getByRole('button', { name: `Pin ${name(40)}` }).click();
        await expect(
          row.getByRole('button', { name: `Pin ${name(40)}` }),
        ).toHaveAttribute('aria-pressed', 'true');
        // Undo by keyboard: focus stays on the star.
        await expect(page.getByRole('status')).toContainText(
          'Control Z undoes it.',
        );
        await shot('undo-toast');
        await page.keyboard.press('Control+z');
        await expect(
          page.getByRole('status').getByText('Undone.'),
        ).toBeVisible();
        await expect(
          row.getByRole('button', { name: `Pin ${name(40)}` }),
        ).toHaveAttribute('aria-pressed', 'false');
        await expect(
          row.getByRole('button', { name: `Pin ${name(40)}` }),
        ).toBeFocused();
        await row.getByRole('button', { name: `Pin ${name(40)}` }).click();
        await expect(
          row.getByRole('button', { name: `Pin ${name(40)}` }),
        ).toHaveAttribute('aria-pressed', 'true');
      }
      await more.click();
      await page.getByRole('menuitem', { name: 'Edit tags…' }).click();
      const tags = page.getByRole('dialog', { name: `Tags for ${name(40)}` });
      await tags
        .getByRole('searchbox', { name: 'Find or create a tag' })
        .fill(`${prefix}-new`);
      await tags
        .getByRole('menuitem', { name: `Create tag “${prefix}-new”` })
        .click();
      await expect(
        page
          .getByRole('status')
          .getByText(`Added ${prefix}-new to ${name(40)}`),
      ).toBeVisible();
      await page.keyboard.press('Escape');
      await expect(row.getByText(`${prefix}-new`)).toBeVisible();
      await shot('row-menu');
    });
  });
}
