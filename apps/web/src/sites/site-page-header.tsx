// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient } from '@wardlock/apiclient-ts';
import { CopyField, Notice, StatusTag } from '@wardlock/ui';
import { useEffect, useState } from 'react';
import { AccessWord, Time, type Policy, type Site } from './format.js';
import { OpenSiteButton } from './open-site.js';
import { Organisation, type Organise } from './site-page-organise.js';
import { readWeek } from './week-analytics.js';

/**
 * The site page's header: access, name with its pin, address, group and
 * tags, when it went live, Open site, and the notice for a site the
 * operator has taken down.
 */
export function SiteHeader({
  api,
  site,
  policy,
  organise,
  onLinkCreated,
}: {
  api: ApiClient;
  site: Site;
  policy: Policy | undefined;
  organise: Organise;
  onLinkCreated: () => void;
}) {
  return (
    <>
      <header className="site-header">
        <div className="site-header-main">
          <p className="site-header-access">
            <AccessWord policy={policy} />
            {site.unavailable && (
              <StatusTag tone="warning">Taken down</StatusTag>
            )}
          </p>
          <div className="site-header-title">
            <h1>{site.name}</h1>
            <button
              type="button"
              className="site-pin"
              aria-pressed={site.pinned}
              aria-label="Pin site"
              title={site.pinned ? 'Pinned' : 'Not pinned'}
              onClick={organise.togglePin}
            >
              <Star filled={site.pinned} />
            </button>
          </div>
          <CopyField label="Site address" value={site.url} />
        </div>
        <div className="site-header-side">
          <Organisation site={site} organise={organise} />
          <p className="site-header-meta">
            <WeekRequests api={api} name={site.name} />
            {site.activatedAt !== undefined ? (
              <>
                Live since <Time at={site.activatedAt} />
              </>
            ) : (
              'Not deployed yet'
            )}
          </p>
          {!site.unavailable && (
            // Visitors see only the unavailable page, so there is nothing
            // to open.
            <OpenSiteButton
              api={api}
              site={site}
              policy={policy}
              onLinkCreated={onLinkCreated}
            />
          )}
        </div>
      </header>
      {organise.error && <Notice tone="danger">{organise.error}</Notice>}
      {site.unavailable && (
        <Notice tone="warning">
          <strong>The operator of this server has taken this site down.</strong>{' '}
          Visitors see an unavailable page at every address of the site. Its
          deploys, domains and access records are kept as they are: you can
          change who has access, but not deploy, roll back, change its domains,
          delete a policy or tear it down until the operator restores it.
        </Notice>
      )}
    </>
  );
}

const count = new Intl.NumberFormat('en-NZ');

/** "1,204 requests in 7 days", with the refused ones named when there are any. */
export function weekRequestsText(allowed: number, refused: number) {
  const total = allowed + refused;
  const words = `${count.format(total)} ${total === 1 ? 'request' : 'requests'} in 7 days`;
  return refused > 0 ? `${words} (${count.format(refused)} refused)` : words;
}

/**
 * The site's requests over the last 7 days, allowed and refused, followed by
 * a separator. Nothing shows while it loads or if it cannot be read: the
 * count is a detail, never a reason for the page to fail.
 */
function WeekRequests({ api, name }: { api: ApiClient; name: string }) {
  const [total, setTotal] = useState<{
    allowed: number;
    refused: number;
  } | null>(null);
  useEffect(() => {
    let live = true;
    readWeek(api, name).then(
      (data) => live && setTotal(data.total),
      () => undefined,
    );
    return () => {
      live = false;
    };
  }, [api, name]);
  if (!total) return null;
  return (
    <>
      <span className="site-header-requests">
        {weekRequestsText(total.allowed, total.refused)}
      </span>
      {' · '}
    </>
  );
}

/** The pin glyph: filled while pinned. Decorative. */
export function Star({ filled }: { filled: boolean }) {
  return (
    <svg
      viewBox="0 0 16 16"
      fill={filled ? 'currentColor' : 'none'}
      stroke="currentColor"
      strokeWidth="1.5"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      <path d="M8 1.75 9.9 5.6l4.25.62-3.07 3 .72 4.23L8 11.45l-3.8 2 .72-4.23-3.07-3 4.25-.62Z" />
    </svg>
  );
}
