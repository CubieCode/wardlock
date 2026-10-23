// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only
import { expect, test, type Page, type TestInfo } from '@playwright/test';
import { firstOwnerState, schemes } from './mail.js';
import { goToPlace, isMobile } from './places.js';
import { createSite } from './publish.js';

const origin = process.env.WARDLOCK_ACCEPTANCE_ORIGIN;
if (!origin) throw new Error('Run through pnpm browser:test');
const headers = { Origin: origin };

// The first owner's saved session: email-code sends are limited per IP, and
// the other specs already spend most of that limit signing in. Names carry
// the project and scheme, since every run of this spec shares the owner.
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

for (const scheme of schemes) {
  test(`organise: groups and tags (${scheme})`, async ({ page }, info) => {
    await page.emulateMedia({ colorScheme: scheme });
    const shot = (step: string) =>
      check(page, info, `organise-${step}-${scheme}`);
    const lane = `${info.project.name}-${scheme}`;
    const alpha = `organise-${lane}-alpha`;
    const beta = `organise-${lane}-beta`;
    await createSite(page.request, alpha);
    await createSite(page.request, beta);

    const groupName = `Clients ${lane}`;
    const tagName = `docs-${lane}`;
    const groups = page.getByRole('list', { name: 'Groups' });
    const tags = page.getByRole('list', { name: 'Tags' });
    const groupRow = (name: string) =>
      groups.getByRole('listitem').filter({ hasText: name });
    const tagRow = (name: string) =>
      tags.getByRole('listitem').filter({ hasText: name });

    await test.step('the rail opens Groups and tags', async () => {
      await page.goto(`${origin}/sites`);
      const main = page.getByRole('navigation', { name: 'Main' });
      await goToPlace(page, info, 'Groups and tags');
      await expect(page).toHaveURL(`${origin}/organise`);
      await expect(
        page.getByRole('heading', { level: 1, name: 'Groups and tags' }),
      ).toBeVisible();
      if (isMobile(info)) {
        // From the Account sheet: the sheet closes, and the Account tab and
        // the sheet's entry both say this is the current page.
        const account = main.getByRole('button', { name: 'Account' });
        await expect(page.getByRole('dialog', { name: 'Account' })).toHaveCount(
          0,
        );
        await expect(account).toHaveAttribute('aria-current', 'page');
        await account.click();
        await expect(
          page
            .getByRole('navigation', { name: 'More places' })
            .getByRole('link', { name: 'Groups and tags' }),
        ).toHaveAttribute('aria-current', 'page');
        await shot('account-sheet');
        await page.keyboard.press('Escape');
      } else {
        await expect(
          main.getByRole('link', { name: 'Groups and tags' }),
        ).toHaveAttribute('aria-current', 'page');
      }
    });

    await test.step('creates a group and a tag', async () => {
      await page.getByRole('button', { name: 'New group' }).click();
      const field = page.getByRole('textbox', { name: 'New group name' });
      await expect(field).toBeFocused();
      await field.fill(groupName);
      await page.getByRole('button', { name: 'Create group' }).click();
      await expect(
        groupRow(groupName).getByRole('link', {
          name: `0 sites in ${groupName}`,
        }),
      ).toBeVisible();

      await page.getByRole('button', { name: 'New tag' }).click();
      await page.getByRole('textbox', { name: 'New tag name' }).fill(tagName);
      await page.getByRole('button', { name: 'Create tag' }).click();
      await expect(
        tagRow(tagName).getByRole('link', {
          name: `0 sites tagged ${tagName}`,
        }),
      ).toBeVisible();
    });

    await test.step('shows the server’s validation errors inline', async () => {
      await page.getByRole('button', { name: 'New group' }).click();
      const field = page.getByRole('textbox', { name: 'New group name' });
      await field.fill(groupName.toUpperCase());
      await page.getByRole('button', { name: 'Create group' }).click();
      await expect(page.getByRole('alert')).toHaveText(
        'You already have a group with this name.',
      );
      await expect(field).toHaveAttribute('aria-invalid', 'true');
      await shot('name-taken');
      await field.fill('x'.repeat(41));
      await page.getByRole('button', { name: 'Create group' }).click();
      await expect(page.getByRole('alert')).toHaveText(/1-40 characters/);
      await page.getByRole('button', { name: 'Cancel' }).click();
      await expect(field).toHaveCount(0);

      await page.getByRole('button', { name: 'New tag' }).click();
      await page
        .getByRole('textbox', { name: 'New tag name' })
        .fill('Not Valid');
      await page.getByRole('button', { name: 'Create tag' }).click();
      await expect(page.getByRole('alert')).toHaveText(/a-z, 0-9 and '-'/);
      await page.keyboard.press('Escape');
      await expect(page.getByRole('textbox')).toHaveCount(0);
    });

    await test.step('counts link to the filtered sites list', async () => {
      const list = async (path: string) =>
        (await (
          await page.request.get(`${origin}/api/v0/${path}`, { headers })
        ).json()) as { items: { id: string; name: string }[] };
      const groupId = (await list('groups')).items.find(
        (g) => g.name === groupName,
      )!.id;
      const tagId = (await list('tags')).items.find(
        (t) => t.name === tagName,
      )!.id;
      for (const [site, tagIds] of [
        [alpha, [tagId]],
        [beta, []],
      ] as const) {
        const res = await page.request.put(
          `${origin}/api/v0/sites/${site}/organisation`,
          { data: { groupId, tagIds }, headers },
        );
        expect(res.status()).toBe(200);
      }
      await page.reload();
      const groupLink = groupRow(groupName).getByRole('link', {
        name: `2 sites in ${groupName}`,
      });
      await expect(groupLink).toHaveAttribute('href', `/sites?g=${groupId}`);
      await expect(
        tagRow(tagName).getByRole('link', { name: `1 site tagged ${tagName}` }),
      ).toHaveAttribute('href', `/sites?t=${tagId}`);
      await shot('page');
      const shown = page
        .getByRole('main')
        .getByRole('link', { name: new RegExp(`^organise-${lane}-`) });
      const filters = page.getByRole('group', { name: 'Filters' });

      // The group's count opens the list of just its two sites.
      await groupLink.click();
      await expect(page).toHaveURL(`${origin}/sites?g=${groupId}`);
      await expect(filters).toContainText(groupName);
      await expect(shown).toHaveText([alpha, beta]);
      await page.goBack();

      // The tag's count opens the list of just the site with that tag.
      await tagRow(tagName)
        .getByRole('link', { name: `1 site tagged ${tagName}` })
        .click();
      await expect(page).toHaveURL(`${origin}/sites?t=${tagId}`);
      await expect(filters).toContainText(tagName);
      await expect(shown).toHaveText([alpha]);
      await page.goBack();
      await expect(groupLink).toBeVisible();
    });

    await test.step('chooses a tag colour, always beside the name', async () => {
      const trigger = page.getByRole('button', {
        name: `Colour of ${tagName}`,
      });
      await trigger.click();
      const colours = page.getByRole('group', {
        name: `Colour for ${tagName}`,
      });
      await expect(colours.getByRole('radio')).toHaveCount(9);
      // Each choice shows the tag itself in that colour.
      await expect(colours.locator('.wl-tag-chip')).toHaveText(
        Array(9).fill(tagName),
      );
      await shot('colours');
      const five = colours.getByRole('radio', { name: 'Colour 5' });
      await five.check();
      await expect(tagRow(tagName)).toContainText('Colour 5');
      await expect(five).toBeChecked();
      await expect(five).toBeFocused();
      await page.keyboard.press('Escape');
      await expect(colours).toHaveCount(0);
      await expect(trigger).toBeFocused();
    });

    const renamed = `Agency ${lane}`;
    await test.step('renames a group', async () => {
      await page.getByRole('button', { name: `Rename ${groupName}` }).click();
      const field = page.getByRole('textbox', { name: `Rename ${groupName}` });
      await expect(field).toHaveValue(groupName);
      await field.fill(renamed);
      await page.keyboard.press('Enter');
      await expect(
        page.getByRole('button', { name: `Rename ${renamed}` }),
      ).toBeFocused();
      await expect(
        page.getByRole('link', { name: `2 sites in ${renamed}` }),
      ).toBeVisible();
    });

    await test.step('confirms deleting a group, keeping its sites', async () => {
      await page.getByRole('button', { name: `Delete ${renamed}` }).click();
      const dialog = page.getByRole('dialog', {
        name: `Delete the group “${renamed}”?`,
      });
      await expect(dialog).toContainText('Its 2 sites move to No group.');
      await shot('delete-group');
      await dialog.getByRole('button', { name: 'Delete group' }).click();
      await expect(dialog).toHaveCount(0);
      await expect(groupRow(renamed)).toHaveCount(0);
      await expect(
        page.getByText(`Deleted the group “${renamed}”.`),
      ).toBeVisible();
      // Focus stays in the list: the next row, the previous one, or New group.
      await expect(page.locator(':focus')).toHaveCount(1);
      expect(await page.evaluate(() => document.activeElement?.tagName)).toBe(
        'BUTTON',
      );
      const site = await page.request.get(`${origin}/api/v0/sites/${alpha}`, {
        headers,
      });
      expect(site.status()).toBe(200);
      expect(((await site.json()) as { groupId?: string }).groupId).toBe(
        undefined,
      );
    });

    await test.step('confirms deleting a tag', async () => {
      await page.getByRole('button', { name: `Delete ${tagName}` }).click();
      const dialog = page.getByRole('dialog', {
        name: `Delete the tag “${tagName}”?`,
      });
      await expect(dialog).toContainText('Its site loses the tag.');
      await dialog.getByRole('button', { name: 'Cancel' }).click();
      await expect(dialog).toHaveCount(0);
      await expect(tagRow(tagName)).toHaveCount(1);
      await page.getByRole('button', { name: `Delete ${tagName}` }).click();
      await dialog.getByRole('button', { name: 'Delete tag' }).click();
      await expect(tagRow(tagName)).toHaveCount(0);
    });
  });
}
