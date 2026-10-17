// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Reads the throwaway server's dev:// mail sink (scripts/browser-acceptance.mjs
// sets WARDLOCK_ACCEPTANCE_MAIL_DIR). Every address is synthetic.
import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { expect } from '@playwright/test';

const dir = process.env.WARDLOCK_ACCEPTANCE_MAIL_DIR;
if (!dir) throw new Error('Run through pnpm browser:test');

/** The decoded text of every message sent to `address`, oldest first. */
function messagesTo(address: string) {
  let names: string[];
  try {
    names = readdirSync(dir!).sort();
  } catch {
    return [];
  }
  return names
    .map((name) => readFileSync(join(dir!, name), 'utf8'))
    .filter((raw) => new RegExp(`^To: .*<?${address}>?\\r?$`, 'mi').test(raw))
    .map((raw) =>
      // Quoted-printable: soft line breaks, then escaped bytes.
      raw
        .replace(/=\r?\n/g, '')
        .replace(/=([0-9A-F]{2})/g, (_, hex: string) =>
          String.fromCharCode(parseInt(hex, 16)),
        ),
    );
}

/**
 * Reads `address`'s messages in order: each call returns the next one not yet
 * read, waiting for it to arrive.
 */
export function mailbox(address: string) {
  let seen = 0;
  const next = async () => {
    let message = '';
    await expect
      .poll(() => {
        const all = messagesTo(address);
        message = all[seen] ?? '';
        return message !== '';
      })
      .toBe(true);
    seen += 1;
    return message;
  };
  return {
    /** The 6-digit code in the next sign-in email. */
    async code() {
      const match = /^\s+(\d{6})\s*$/m.exec(await next());
      if (!match) throw new Error('no code in the sign-in email');
      return match[1]!;
    },
    /** The one-time sign-in URL in the next invitation. */
    async signInLink() {
      const match = /https?:\/\/\S+\/sign-in\?code=[\w-]+/.exec(await next());
      if (!match) throw new Error('no sign-in link in the invitation');
      return match[0];
    },
  };
}

/** The synthetic owner each project and theme signs in as. */
export const ownerFor = (project: string, scheme: string) =>
  `${project}-${scheme}@example.test`;

/**
 * The synthetic owner each project and theme adds passkeys as (C1.6). No
 * address may end with another, since messagesTo matches the end of To.
 */
export const passkeyOwnerFor = (project: string, scheme: string) =>
  `${project}-${scheme}-passkeys@example.test`;

/** The synthetic owner the CLI login spec signs in as. */
export const cliOwnerFor = (project: string, scheme: string) =>
  `${project}-${scheme}-cli@example.test`;

export const projects = ['desktop', 'mobile'] as const;
export const schemes = ['light', 'dark'] as const;
