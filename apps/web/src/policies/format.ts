// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { components } from '@wardlock/apiclient-ts';
import type { Policy } from '../sites/format.js';

export type PolicyMethod = components['schemas']['PolicyMethod'];
export type SetPolicyRequest = components['schemas']['SetPolicyRequest'];

/** Every method, in the order the editor offers them. */
export const methods: PolicyMethod[] = ['links', 'password', 'email', 'oauth'];

/** Each method as a noun phrase for summaries. */
export const methodNames: Record<PolicyMethod, string> = {
  links: 'share links',
  password: 'password',
  email: 'email code',
  oauth: 'GitHub, Google or single sign-on',
};

/** "share links, password and email code". */
export function listText(items: string[]) {
  return new Intl.ListFormat('en-NZ', { type: 'conjunction' }).format(items);
}

/** How a protected policy lets visitors in, as a sentence fragment. */
export const methodsText = (policy: Policy) =>
  listText(
    methods
      .filter((m) => policy.methods.includes(m))
      .map((m) => methodNames[m]),
  );

/** "the site default" or "/docs/". */
export const policyName = (path: string) =>
  path === '/' ? 'the site default' : path;

/** Whether `prefix` governs `path`: the path itself, under it, or its directory without the slash. */
function covers(prefix: string, path: string) {
  return (
    prefix === '/' || path.startsWith(prefix) || path === prefix.slice(0, -1)
  );
}

/**
 * The policy that decides a request for `path`: the longest matching
 * prefix, falling back to the site default. Policies never combine (SPEC
 * "Policies").
 */
export function governing(policies: Policy[], path: string) {
  let best: Policy | undefined;
  for (const policy of policies)
    if (
      covers(policy.path, path) &&
      (!best || policy.path.length > best.path.length)
    )
      best = policy;
  return best;
}

/** The policy a path policy's paths fall back to once it is deleted. */
export const fallback = (policies: Policy[], deleted: Policy) =>
  governing(
    policies.filter((p) => p.id !== deleted.id),
    deleted.path,
  );

/** Policies in the order the server lists them: by path, the default first. */
export const byPath = (a: Policy, b: Policy) =>
  a.path < b.path ? -1 : a.path > b.path ? 1 : 0;

/**
 * The allowlist's addresses and domains, typed one per line (commas,
 * semicolons and spaces also separate them). Anything with an `@` is an
 * address; the server checks and normalises every entry.
 */
export function parseEntries(text: string) {
  const entries = text.split(/[\s,;]+/).filter(Boolean);
  return {
    emails: entries.filter((e) => e.includes('@')),
    domains: entries.filter((e) => !e.includes('@')),
  };
}

/** GitHub organisation logins, one per line or separated as above. */
export const parseOrgs = (text: string) =>
  text.split(/[\s,;]+/).filter(Boolean);

/** Every allowlist entry of a policy, addresses first. */
export const allowlistEntries = (policy: Policy) => [
  ...policy.allowlist.emails,
  ...policy.allowlist.domains,
  ...(policy.allowlist.githubOrgs ?? []).map((org) => `GitHub: ${org}`),
];

/** "ana@example.com, example.com and 3 more". */
export function allowlistText(policy: Policy, shown = 2) {
  const entries = allowlistEntries(policy);
  if (entries.length <= shown + 1) return listText(entries);
  return `${entries.slice(0, shown).join(', ')} and ${entries.length - shown} more`;
}

/** Character count, as the server counts a password's length. */
export const characters = (text: string) => [...text].length;

/** UTF-8 size, for the server's 1,024-byte password limit. */
export const bytes = (text: string) => new TextEncoder().encode(text).length;
