// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient } from '@wardlock/apiclient-ts';
import { Button, Notice, PageHeader } from '@wardlock/ui';
import { useState, type ReactNode } from 'react';
import type { Policy, Site } from './format.js';

/** How long the share link behind Open site keeps working. */
export const openLinkLifetime = 10 * 60_000;
export const openLinkLabel = 'Opened from dashboard';

/**
 * The site page's heading with its Open site button. A public site opens at
 * its address; any other site opens through a new short-lived share link,
 * since dashboard sign-in does not reach the site's domain.
 */
export function SiteHeading({
  api,
  site,
  policy,
  description,
  onLinkCreated,
}: {
  api: ApiClient;
  site: Site;
  /** The site's `/` policy; unknown is treated as not public. */
  policy: Policy | undefined;
  description: ReactNode;
  onLinkCreated: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  const open = async () => {
    setFailure(null);
    if (policy?.visibility === 'public') {
      window.open(site.url, '_blank', 'noopener');
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
    tab.location.href = result.data.url;
    onLinkCreated();
  };

  return (
    <>
      <div className="site-heading">
        <PageHeader title={site.name} description={description} />
        <Button type="button" disabled={busy} onClick={open}>
          {busy ? 'Opening…' : 'Open site'}
        </Button>
      </div>
      {failure && <Notice tone="danger">{failure}</Notice>}
    </>
  );
}
