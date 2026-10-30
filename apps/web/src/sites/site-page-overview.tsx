// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient } from '@wardlock/apiclient-ts';
import { Card, Notice, StatusTag } from '@wardlock/ui';
import { useEffect, useState, type ReactNode } from 'react';
import { DomainStatus, type Domain } from '../domains/format.js';
import { expiryText, LinkStatus, type ShareLink } from '../links/format.js';
import { methodsText } from '../policies/format.js';
import {
  AccessWord,
  shortId,
  Time,
  type Deploy,
  type Policy,
  type Site,
} from './format.js';
import { deployFacts } from './site-page-deploys.js';
import { siteHref } from './site-page-tabs.js';

/** How many share links the overview shows; the Links tab has them all. */
const newestLinks = 3;

type Preview<T> =
  { kind: 'loading' } | { kind: 'error' } | { kind: 'ready'; items: T[] };

/** Settles a preview from one read of a list. */
function settle<T>(
  read: Promise<{ data?: { items: T[] } }>,
  set: (state: Preview<T>) => void,
) {
  let live = true;
  read.then(
    ({ data }) =>
      live &&
      set(data ? { kind: 'ready', items: data.items } : { kind: 'error' }),
    () => live && set({ kind: 'error' }),
  );
  return () => {
    live = false;
  };
}

/** The Overview tab: who can open it, links, domains and the live deploy. */
export function OverviewTab({
  api,
  site,
  policies,
  deploys,
  linksVersion,
  refreshNotice,
}: {
  api: ApiClient;
  site: Site;
  policies: Policy[];
  deploys: Deploy[];
  /** Changes when a share link is created elsewhere on the page. */
  linksVersion: number;
  /** Shown in the access card when a refresh after a change failed. */
  refreshNotice: ReactNode;
}) {
  const [links, setLinks] = useState<Preview<ShareLink>>({ kind: 'loading' });
  const [domains, setDomains] = useState<Preview<Domain>>({
    kind: 'loading',
  });
  useEffect(
    () =>
      settle(
        api.GET('/api/v0/sites/{site}/links', {
          params: { path: { site: site.name }, query: { limit: newestLinks } },
        }),
        setLinks,
      ),
    [api, site.name, linksVersion],
  );
  useEffect(
    () =>
      settle(
        api.GET('/api/v0/sites/{site}/domains', {
          params: { path: { site: site.name } },
        }),
        setDomains,
      ),
    [api, site.name],
  );

  return (
    <div className="site-overview">
      <Card title="Who can open it">
        <p>{accessSummary(policies.find((p) => p.path === '/'))}</p>
        <ul className="site-mini-list" aria-label="Access by path">
          {policies.map((policy) => (
            <li key={policy.id}>
              <code>{policy.path}</code>
              <AccessWord policy={policy} />
              <span className="site-muted">
                {policy.visibility === 'public'
                  ? 'Anyone with the address'
                  : methodsText(policy)}
              </span>
            </li>
          ))}
        </ul>
        {refreshNotice}
        <CardLinks>
          <a href={siteHref(site.name, 'access')}>Manage access</a>
        </CardLinks>
      </Card>

      <Card title="Share links">
        {links.kind === 'loading' && <p role="status">Loading share links…</p>}
        {links.kind === 'error' && (
          <Notice tone="danger">
            Could not load the share links. Open Links to try again.
          </Notice>
        )}
        {links.kind === 'ready' &&
          (links.items.length === 0 ? (
            <p className="site-muted">No share links yet.</p>
          ) : (
            <ul className="site-mini-list" aria-label="Newest share links">
              {links.items.map((link) => (
                <li key={link.id}>
                  <a href={siteHref(site.name, `links/${link.publicId}`)}>
                    {link.label ?? <code>{link.publicId}</code>}
                  </a>
                  <LinkStatus link={link} />
                  <span className="site-muted">{expiryText(link)}</span>
                </li>
              ))}
            </ul>
          ))}
        <CardLinks>
          <a href={siteHref(site.name, 'links')}>Manage share links</a>
        </CardLinks>
      </Card>

      <Card title="Domains">
        <ul className="site-mini-list" aria-label="Addresses">
          <li>
            <code>{site.hostname}</code>
            <StatusTag>Default</StatusTag>
            {site.primaryHost === site.hostname && (
              <StatusTag tone="primary">Primary</StatusTag>
            )}
          </li>
          {domains.kind === 'ready' &&
            domains.items.map((domain) => (
              <li key={domain.id}>
                <code>{domain.hostname}</code>
                <DomainStatus domain={domain} />
                {domain.primary && (
                  <StatusTag tone="primary">Primary</StatusTag>
                )}
              </li>
            ))}
        </ul>
        {domains.kind === 'loading' && (
          <p role="status">Loading custom domains…</p>
        )}
        {domains.kind === 'error' && (
          <Notice tone="danger">
            Could not load the custom domains. Open Domains to try again.
          </Notice>
        )}
        <CardLinks>
          <a href={siteHref(site.name, 'domains')}>Manage domains</a>
        </CardLinks>
      </Card>

      <Card title="Live deploy">
        <LiveDeploy site={site} deploys={deploys} />
        <CardLinks>
          <a href={siteHref(site.name, 'deploys')}>Deploy history</a>
        </CardLinks>
      </Card>
    </div>
  );
}

function CardLinks({ children }: { children: ReactNode }) {
  return <p className="site-card-links">{children}</p>;
}

function LiveDeploy({ site, deploys }: { site: Site; deploys: Deploy[] }) {
  if (!site.activeDeployId)
    return (
      <p className="site-muted">
        Not deployed yet. Deploy with the CLI and the live deploy shows here.
      </p>
    );
  // The live deploy may be older than the newest page of the history.
  const live = deploys.find((d) => d.id === site.activeDeployId);
  return (
    <div className="site-live-deploy">
      <p>
        <code>{shortId(site.activeDeployId)}</code>{' '}
        {live && (
          <span className={live.message ? undefined : 'site-muted'}>
            {live.message || 'No message'}
          </span>
        )}
      </p>
      <p className="site-muted">
        {site.activatedAt !== undefined && (
          <>
            Live since <Time at={site.activatedAt} />
          </>
        )}
        {live && <> · {deployFacts(live)}</>}
      </p>
    </div>
  );
}

export function accessSummary(policy: Policy | undefined) {
  if (!policy) return 'Could not read who can open this site.';
  if (policy.visibility === 'public')
    return 'Anyone with the address can open this site.';
  return 'Only visitors with access can open this site.';
}
