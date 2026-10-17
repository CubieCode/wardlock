// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Plain-language messages for the failures the sign-in screens share.

export const unreachable =
  'Could not reach the server. Check your connection, then try again.';

export const serverError =
  'Something went wrong on the server. Try again in a moment.';

/** The 429 message, using Retry-After when the server sends it. */
export function rateLimited(response: Response) {
  const seconds = Number(response.headers.get('Retry-After'));
  const minutes = seconds > 0 ? Math.ceil(seconds / 60) : 1;
  return `Too many attempts. Try again in ${minutes === 1 ? '1 minute' : `${minutes} minutes`}.`;
}

/** The message for a failed call that is neither a field error nor a 429. */
export function failure(response: Response | undefined) {
  if (!response) return unreachable;
  if (response.status === 429) return rateLimited(response);
  return serverError;
}
