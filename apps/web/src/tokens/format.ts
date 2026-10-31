// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { components } from '@wardlock/apiclient-ts';
import { scopeChoices } from '../cli-login/cli-login.js';

export type ApiToken = components['schemas']['ApiToken'];
export type CreatedApiToken = components['schemas']['CreatedApiToken'];

const day = 24 * 3600_000;

/** How close to its expiry a token is marked on the list. */
const expiringWithin = 7 * day;

const dateFormat = new Intl.DateTimeFormat('en-NZ', {
  day: 'numeric',
  month: 'short',
  year: 'numeric',
});

export const formatDate = (at: number) => dateFormat.format(new Date(at));

export const scopesText = (token: ApiToken) =>
  scopeChoices
    .filter((choice) => token.scopes.includes(choice.scope))
    .map((choice) => choice.label)
    .join(', ');

export const expiringSoon = (token: ApiToken, now = Date.now()) =>
  token.expiresAt !== undefined && token.expiresAt - now <= expiringWithin;
