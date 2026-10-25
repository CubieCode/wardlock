// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Moving around a site's page (V5.10): its tabs are links under the header.
import { expect, type Page, type TestInfo } from '@playwright/test';

/** Opens one of the site page's tabs and waits for it to be current. */
export async function siteTab(page: Page, name: string) {
  const tab = page
    .getByRole('navigation', { name: 'Site sections' })
    .getByRole('link', { name, exact: true });
  await tab.click();
  await expect(tab).toHaveAttribute('aria-current', 'page');
}

/** Whether the test runs in the phone-sized project. */
export const onPhone = (info: TestInfo) => info.project.name === 'mobile';

/**
 * A requests log by its caption: a table from 48rem, requests grouped by day
 * below it.
 */
export function visitLog(page: Page, name: string | RegExp, phone: boolean) {
  return page.getByRole(phone ? 'region' : 'table', { name });
}

/** The log's requests, newest first: its table rows or its day rows. */
export function visitRows(page: Page, name: string | RegExp, phone: boolean) {
  const log = visitLog(page, name, phone);
  return phone
    ? log.getByRole('listitem')
    : log.getByRole('row').filter({ hasNot: page.getByRole('columnheader') });
}
