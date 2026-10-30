// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient } from '@wardlock/apiclient-ts';
import { Button, Notice } from '@wardlock/ui';
import { useState, type ReactNode } from 'react';
import type { Policy, Site } from './format.js';

/** How long the share link behind Open site keeps working. */
export const openLinkLifetime = 10 * 60_000;
export const openLinkLabel = 'Opened from dashboard';

/**
 * `link`, a share link's URL, on the host of `address`: a site-wide link
 * opens every hostname of its site, previews included.
 */
export function onHostOf(link: string, address: string) {
  const url = new URL(link);
  const host = new URL(address);
  url.protocol = host.protocol;
  url.host = host.host;
  return url.href;
}

/**
 * The site header's Open site button. A public site opens at its address;
 * any other site opens through a new short-lived share link, since
 * dashboard sign-in does not reach the site's domain. A taken-down site has
 * nothing to open, so the header leaves the button out. With `url`, it opens
 * another address of the site, such as a preview, the same way.
 */
export function OpenSiteButton({
  api,
  site,
  policy,
  onLinkCreated,
  url = site.url,
  label,
  children = 'Open site',
}: {
  api: ApiClient;
  site: Site;
  /** The site's `/` policy; unknown is treated as not public. */
  policy: Policy | undefined;
  onLinkCreated: () => void;
  /** The address to open; the site's own by default. */
  url?: string;
  /** The button's accessible name, when its text needs context. */
  label?: string;
  children?: ReactNode;
}) {
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  const open = async () => {
    setFailure(null);
    if (policy?.visibility === 'public') {
      window.open(url, '_blank', 'noopener');
      return;
    }
    // Opened now, inside the click, so pop-up blockers allow it; the share
    // link's address follows once it exists. With `noopener` the browser
    // would not return the tab, so the opener is cut by hand instead.
    const tab = window.open('', '_blank');
    if (!tab)
      return setFailure(
        'Your browser blocked the new tab. Allow pop-ups for this dashboard, then try again.',
      );
    tab.opener = null;
    setBusy(true);
    const result = await api
      .POST('/api/v0/sites/{site}/links', {
        params: { path: { site: site.name } },
        body: {
          label: openLinkLabel,
          path: '/',
          expiresAt: Date.now() + openLinkLifetime,
        },
      })
      .catch(() => null);
    setBusy(false);
    if (!result?.data) {
      tab.close();
      return setFailure(
        result?.error?.message
          ? `Could not open the site: ${result.error.message}`
          : 'Could not open the site. Check your connection, then try again.',
      );
    }
    tab.location.href = onHostOf(result.data.url, url);
    onLinkCreated();
  };

  return (
    <div className="site-open">
      <Button type="button" disabled={busy} aria-label={label} onClick={open}>
        {busy ? 'Opening…' : children}
      </Button>
      {failure && <Notice tone="danger">{failure}</Notice>}
    </div>
  );
}
