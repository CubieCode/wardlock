// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// The shell's places: every built screen is in the rail from 48rem; below
// it the bottom bar holds Overview, Sites and Analytics (with Jump to and
// Account) and the rest are listed in the Account sheet.
import type { Page, TestInfo } from '@playwright/test';

/** The places in the rail, in order. */
export const railPlaces = [
  'Overview',
  'Sites',
  'Analytics',
  'Groups and tags',
  'Passkeys',
  'API tokens',
  'Notifications',
];
/** The pages Jump to lists: the rail's, with the sites map after Sites. */
export const jumpPages = [
  'Overview',
  'Sites',
  'Sites map',
  'Analytics',
  'Groups and tags',
  'Passkeys',
  'API tokens',
  'Notifications',
];
/** The places in the bottom bar below 48rem. */
export const barPlaces = ['Overview', 'Sites', 'Analytics'];
/** The places in the Account sheet below 48rem. */
export const sheetPlaces = ['Groups and tags', 'Passkeys', 'API tokens'];
export const sheetPlaces = ['Groups and tags', 'Passkeys', 'Notifications'];

export const isMobile = (info: TestInfo) => info.project.name === 'mobile';

/** Follows a place from the rail, or from the Account sheet below 48rem. */
export async function goToPlace(page: Page, info: TestInfo, name: string) {
  const main = page.getByRole('navigation', { name: 'Main' });
  if (isMobile(info) && sheetPlaces.includes(name)) {
    await main.getByRole('button', { name: 'Account' }).click();
    await page
      .getByRole('navigation', { name: 'More places' })
      .getByRole('link', { name })
      .click();
  } else {
    await main.getByRole('link', { name }).click();
  }
}
