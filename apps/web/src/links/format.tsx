// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { components } from '@wardlock/apiclient-ts';
import { StatusBadge } from '@wardlock/ui';
import { relativeTime } from '../sites/format.js';

export type ShareLink = components['schemas']['ShareLink'];
export type CreatedShareLink = components['schemas']['CreatedShareLink'];
export type AccessLogEntry = components['schemas']['AccessLogEntry'];

/**
 * How a confirmation names a link: its label in quotes, or its public ID.
 * Never the token.
 */
export const linkName = (link: ShareLink) =>
  link.label ? `“${link.label}”` : `link ${link.publicId}`;

/** The link's status as a word. */
export function LinkStatus({ link }: { link: ShareLink }) {
  if (link.status === 'active')
    return <StatusBadge tone="success">Active</StatusBadge>;
  if (link.status === 'expired')
    return <StatusBadge tone="warning">Expired</StatusBadge>;
  return <StatusBadge tone="neutral">Used up</StatusBadge>;
}

/** What the link opens: the whole site or one folder. */
export const scopeText = (path: string) =>
  path === '/' ? 'Whole site' : `Only ${path}`;

export function expiryText(link: ShareLink, now = Date.now()) {
  if (link.expiresAt === undefined) return 'Never expires';
  const when = relativeTime(link.expiresAt, now);
  return link.expiresAt > now ? `Expires ${when}` : `Expired ${when}`;
}

export function usesText(link: ShareLink) {
  if (link.maxUses === undefined) return 'Unlimited uses';
  return link.maxUses === 1
    ? `${link.uses} of 1 use`
    : `${link.uses} of ${link.maxUses} uses`;
}

export function lastOpenedText(link: ShareLink, now = Date.now()) {
  return link.lastAccessedAt === undefined
    ? 'Not opened yet'
    : `Last opened ${relativeTime(link.lastAccessedAt, now)}`;
}
