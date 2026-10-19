// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { components } from '@wardlock/apiclient-ts';
import { StatusBadge } from '@wardlock/ui';
import { relativeTime, Time } from '../sites/format.js';

export type Domain = components['schemas']['Domain'];
export type DnsRecord = components['schemas']['DnsRecord'];
type FailureReason = NonNullable<Domain['failureReason']>;

/** A pending claim is deleted this long after it was added (SPEC). */
export const pendingLifetime = 7 * 24 * 3600_000;

/** The status as a word; the tone only supplements it. */
export function DomainStatus({ domain }: { domain: Domain }) {
  if (domain.status === 'active')
    return <StatusBadge tone="success">Active</StatusBadge>;
  if (domain.status === 'lapsed')
    return <StatusBadge tone="danger">Lapsed</StatusBadge>;
  return <StatusBadge tone="warning">Pending</StatusBadge>;
}

/** What the status means for visitors, in one sentence. */
export function statusSummary(domain: Domain) {
  if (domain.status === 'active')
    return 'Verified. Visitors can open the site at this address.';
  if (domain.status === 'lapsed')
    return 'Not served: the DNS record stopped checking out for 72 hours. Serving resumes on its own once it passes again.';
  return 'Not served yet. Wardlock serves this address once the DNS record checks out.';
}

const reasons: Record<FailureReason, string> = {
  no_record: 'No DNS record found yet.',
  cname_elsewhere: 'The CNAME record points somewhere else.',
  txt_mismatch: 'The TXT record holds a different value.',
  dns_error: 'The DNS lookup failed.',
};

const nextSteps: Record<FailureReason, string> = {
  no_record:
    'Add the record at your DNS provider. New records can take a few minutes to show up.',
  cname_elsewhere:
    'Change the CNAME record to the target shown here, or add the TXT record instead.',
  txt_mismatch:
    'Replace the TXT record’s value with the one shown here. Each claim has its own value.',
  dns_error:
    'This is usually temporary. Check again in a few minutes; if it keeps failing, check that your DNS provider is answering.',
};

/** The last check's failure in plain words: what, the server's detail, then what to do. */
export function failureText(reason: FailureReason) {
  return { headline: reasons[reason], next: nextSteps[reason] };
}

/** "in 5 minutes"; under a minute is "within a minute". */
function untilText(at: number, now = Date.now()) {
  return at - now < 60_000 ? 'within a minute' : relativeTime(at, now);
}

/** When DNS was last checked and when it will be again. */
export function CheckTimes({ domain }: { domain: Domain }) {
  return (
    <>
      {domain.lastCheckedAt !== undefined ? (
        <>
          Last checked <Time at={domain.lastCheckedAt} />
        </>
      ) : (
        'Not checked yet'
      )}
      {' · '}
      {domain.nextCheckAt !== undefined
        ? `Next check ${untilText(domain.nextCheckAt)}`
        : 'Checked only when you ask'}
    </>
  );
}

/** Domains in the server's order: by hostname. */
export const byHostname = (a: Domain, b: Domain) =>
  a.hostname < b.hostname ? -1 : a.hostname > b.hostname ? 1 : 0;
