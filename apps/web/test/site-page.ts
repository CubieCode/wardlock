// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Moving around a site's page (V5.10): its tabs are links under the header.
import { expect, type Page } from '@playwright/test';

/** Opens one of the site page's tabs and waits for it to be current. */
export async function siteTab(page: Page, name: string) {
  const tab = page
    .getByRole('navigation', { name: 'Site sections' })
    .getByRole('link', { name, exact: true });
  await tab.click();
  await expect(tab).toHaveAttribute('aria-current', 'page');
}
