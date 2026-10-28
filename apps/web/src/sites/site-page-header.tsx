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
 * operator has taken down. `compact` (phones, off Overview) keeps the name,
 * pin, Copy address and Open site on one row over a line of access,
 * requests and live time, and leaves the group and tags to Overview.
 */
export function SiteHeader({
  api,
  site,
  policy,
  organise,
  onLinkCreated,
  compact = false,
}: {
  api: ApiClient;
  site: Site;
  policy: Policy | undefined;
  organise: Organise;
  onLinkCreated: () => void;
  compact?: boolean;
}) {
  const title = (
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
  );
  // Visitors of a taken-down site see only the unavailable page, so there
  // is nothing to open.
  const open = !site.unavailable && (
    <OpenSiteButton
      api={api}
      site={site}
      policy={policy}
      onLinkCreated={onLinkCreated}
    />
  );
  const copyAddress = useCopyAddress(site.url);
  const live =
    site.activatedAt !== undefined ? (
      <>
        Live since <Time at={site.activatedAt} />
      </>
    ) : (
      'Not deployed yet'
    );
  return (
    <>
      {compact ? (
        <header className="site-header site-header-compact">
          <div className="site-header-row">
            {title}
            {copyAddress.button}
            {open}
          </div>
          <p className="site-header-meta">
            <span className="site-header-access">
              <AccessWord policy={policy} />
              {site.unavailable && (
                <StatusTag tone="warning">Taken down</StatusTag>
              )}
            </span>
            {' · '}
            <WeekRequests api={api} name={site.name} />
            {live}
          </p>
          {copyAddress.status}
        </header>
      ) : (
        <header className="site-header">
          <div className="site-header-main">
            <p className="site-header-access">
              <AccessWord policy={policy} />
              {site.unavailable && (
                <StatusTag tone="warning">Taken down</StatusTag>
              )}
            </p>
            {title}
            <CopyField label="Site address" value={site.url} />
          </div>
          <div className="site-header-side">
            <Organisation site={site} organise={organise} />
            <p className="site-header-meta">
              <WeekRequests api={api} name={site.name} />
              {live}
            </p>
            {open}
          </div>
        </header>
      )}
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

/**
 * The compact header's Copy address, and what it says under the header.
 * Without a clipboard there is no field to select, so it points to
 * Overview's address field.
 */
function useCopyAddress(url: string) {
  const [state, setState] = useState<'idle' | 'copied' | 'failed'>('idle');
  useEffect(() => {
    if (state !== 'copied') return;
    const timer = setTimeout(() => setState('idle'), 2000);
    return () => clearTimeout(timer);
  }, [state]);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(url);
      setState('copied');
    } catch {
      setState('failed');
    }
  };
  const button = (
    <button
      type="button"
      className="site-copy-address"
      aria-label="Copy site address"
      title="Copy site address"
      onClick={copy}
    >
      <svg
        viewBox="0 0 16 16"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinejoin="round"
        aria-hidden="true"
        focusable="false"
      >
        <rect x="5.25" y="5.25" width="8.5" height="8.5" rx="1.5" />
        <path d="M10.75 5.25V3.75a1.5 1.5 0 0 0-1.5-1.5h-5.5a1.5 1.5 0 0 0-1.5 1.5v5.5a1.5 1.5 0 0 0 1.5 1.5h1.5" />
      </svg>
    </button>
  );
  const status = (
    <p className="site-copy-status" role="status">
      {state === 'copied' ? 'Site address copied.' : null}
      {state === 'failed'
        ? 'Could not copy the address. Copy it from Overview.'
        : null}
    </p>
  );
  return { button, status };
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
