// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { components } from '@wardlock/apiclient-ts';
import { StatusBadge } from '@wardlock/ui';

export type Site = components['schemas']['Site'];
export type Deploy = components['schemas']['Deploy'];
export type Policy = components['schemas']['Policy'];
export type Activation = components['schemas']['Activation'];

const locale = 'en-NZ';
const units: [Intl.RelativeTimeFormatUnit, number][] = [
  ['year', 365 * 24 * 3600_000],
  ['month', 30 * 24 * 3600_000],
  ['week', 7 * 24 * 3600_000],
  ['day', 24 * 3600_000],
  ['hour', 3600_000],
  ['minute', 60_000],
];

/** "3 hours ago"; under a minute is "just now". */
export function relativeTime(at: number, now = Date.now()) {
  const elapsed = now - at;
  for (const [unit, size] of units)
    if (Math.abs(elapsed) >= size)
      return new Intl.RelativeTimeFormat(locale, { numeric: 'auto' }).format(
        -Math.round(elapsed / size),
        unit,
      );
  return 'just now';
}

/** A timestamp shown relatively, with the full date and time on hover. */
export function Time({ at }: { at: number }) {
  const date = new Date(at);
  const full = new Intl.DateTimeFormat(locale, {
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(date);
  return (
    <time dateTime={date.toISOString()} title={full}>
      {relativeTime(at)}
    </time>
  );
}

/** Sizes in SI units: "1.5 MB". */
export function formatBytes(bytes: number) {
  const steps = ['byte', 'kilobyte', 'megabyte', 'gigabyte'];
  let value = bytes;
  let step = 0;
  while (value >= 1000 && step < steps.length - 1) {
    value /= 1000;
    step += 1;
  }
  return new Intl.NumberFormat(locale, {
    style: 'unit',
    unit: steps[step],
    unitDisplay: step === 0 ? 'long' : 'short',
    maximumFractionDigits: step === 0 ? 0 : 1,
  }).format(value);
}

export const fileCount = (count: number) =>
  `${new Intl.NumberFormat(locale).format(count)} ${count === 1 ? 'file' : 'files'}`;

/** The short form of a deploy ID: its last eight characters. */
export const shortId = (id: string) => id.slice(-8);

/** The site's default policy as a word. Shown wherever the site appears. */
export function PolicyBadge({ policy }: { policy: Policy | undefined }) {
  if (!policy) return <StatusBadge tone="neutral">Access unknown</StatusBadge>;
  // Same weight for both: protection is a choice, not a health signal. The
  // lock gives Protected a different shape when scanning a list.
  return policy.visibility === 'public' ? (
    <StatusBadge tone="neutral">Public</StatusBadge>
  ) : (
    <StatusBadge tone="neutral" icon={<Lock />}>
      Protected
    </StatusBadge>
  );
}

function Lock() {
  return (
    <svg
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      strokeLinecap="round"
      focusable="false"
    >
      <rect x="3" y="7" width="10" height="7" rx="1.5" />
      <path d="M5.5 7V5a2.5 2.5 0 0 1 5 0v2" />
    </svg>
  );
}

/** The policy for `/`, which every site has. */
export const defaultPolicy = (policies: Policy[]) =>
  policies.find((policy) => policy.path === '/');
