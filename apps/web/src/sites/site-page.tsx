// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient } from '@wardlock/apiclient-ts';
import { Button, Notice, PageHeader } from '@wardlock/ui';
import { useEffect, useRef, useState } from 'react';
import { DomainsCard } from '../domains/domains.js';
import { LinkDetail } from '../links/access-log-page.js';
import { ShareLinksCard } from '../links/share-links.js';
import { AccessTab } from '../policies/providers.js';
import { useTitle } from '../shell/page.js';
import { useWide } from '../shell/wide.js';
import { SiteAccessLogTab } from './access-log-page.js';
import {
  defaultPolicy,
  type Activation,
  type Deploy,
  type Policy,
  type Site,
} from './format.js';
import { DeploysTab } from './site-page-deploys.js';
import { SiteHeader } from './site-page-header.js';
import { useOrganise } from './site-page-organise.js';
import { PreviewsTab } from './previews.js';
import { OverviewTab } from './site-page-overview.js';
import { SettingsTab } from './site-page-settings.js';
import { siteTabs, SiteTabs, type SiteTabId } from './site-page-tabs.js';
import './sites.css';
import './site-page.css';

interface Loaded {
  site: Site;
  policies: Policy[];
  deploys: Deploy[];
  nextCursor: string | undefined;
}

type PageState =
  | { kind: 'loading' }
  | { kind: 'not-found' }
  | { kind: 'error' }
  | ({ kind: 'ready' } & Loaded);

class NotFound extends Error {}

/**
 * The site, its policies and the newest page of deploys. A missing site and
 * another owner's site both answer 404, so both become NotFound; so does a
 * name the server refuses to parse (400).
 */
async function loadSite(api: ApiClient, name: string): Promise<Loaded> {
  const path = { site: name };
  const [site, policies, deploys] = await Promise.all([
    api.GET('/api/v0/sites/{site}', { params: { path } }),
    api.GET('/api/v0/sites/{site}/policies', { params: { path } }),
    api.GET('/api/v0/sites/{site}/deploys', { params: { path } }),
  ]);
  if ([404, 400].includes(site.response.status)) throw new NotFound();
  if (!site.data || !policies.data || !deploys.data)
    throw new Error('could not load the site');
  return {
    site: site.data,
    policies: policies.data.items,
    deploys: deploys.data.items,
    nextCursor: deploys.data.nextCursor,
  };
}

/** The tab's part of the page title; the overview's is the name alone. */
function pageTitle(name: string, tab: SiteTabId, linkId: string | undefined) {
  if (linkId) return `Link access log · ${name}`;
  if (tab === 'overview') return name;
  return `${siteTabs.find((t) => t.id === tab)!.label} · ${name}`;
}

/**
 * `/sites/:name` and its tabs: a header with the site's access, address,
 * group, tags and pin, then Overview, Links (and one link at
 * `links/:linkId`), Access, Domains, Deploys, Previews, Access log and
 * Settings. The
 * site is read once and kept while the owner moves between tabs.
 */
export function SitePage({
  api,
  name,
  tab,
  linkId,
}: {
  api: ApiClient;
  name: string;
  tab: SiteTabId;
  /** The share link shown on the Links tab, by public ID. */
  linkId?: string;
}) {
  const [state, setState] = useState<PageState>({ kind: 'loading' });
  // Bumped when Open site creates a share link, so link lists reload.
  const [linksVersion, setLinksVersion] = useState(0);
  const [more, setMore] = useState<'idle' | 'loading' | 'error'>('idle');
  // What a failed in-place refresh was for: the deploy history after a
  // rollback, or the site's access after a policy change.
  const [refreshFailed, setRefreshFailed] = useState<
    'deploys' | 'access' | null
  >(null);
  // Bumped by every in-place refresh, so a page of older deploys that was
  // requested before it cannot overwrite the fresh history.
  const generation = useRef(0);
  useTitle(
    state.kind === 'not-found'
      ? 'Site not found'
      : pageTitle(name, tab, linkId),
  );

  // Each attempt loads the page again; Try again starts a new one.
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    let live = true;
    loadSite(api, name).then(
      (loaded) => live && setState({ kind: 'ready', ...loaded }),
      (error: unknown) =>
        live &&
        setState({ kind: error instanceof NotFound ? 'not-found' : 'error' }),
    );
    return () => {
      live = false;
    };
  }, [api, name, attempt]);
  const retry = () => {
    setState({ kind: 'loading' });
    setAttempt((n) => n + 1);
  };

  const setSite = (site: Site) =>
    setState((current) =>
      current.kind === 'ready' ? { ...current, site } : current,
    );
  const placeholder: Site = {
    id: '',
    name,
    hostname: '',
    url: '',
    createdAt: 0,
    unavailable: false,
    tagIds: [],
    pinned: false,
  };
  // Below 48rem every tab but Overview gets the one-row header.
  const wide = useWide();
  const organise = useOrganise(
    api,
    state.kind === 'ready' ? state.site : placeholder,
    setSite,
  );

  if (state.kind === 'loading') return <p role="status">Loading site…</p>;
  if (state.kind === 'not-found')
    return (
      <>
        <PageHeader
          title="Site not found"
          description="There is no site with this name in your account."
        />
        <p>
          <a href="/sites">Go to your sites</a>
        </p>
      </>
    );
  if (state.kind === 'error')
    return (
      <>
        <PageHeader title={name} />
        <Notice tone="danger">
          Could not load this site. Check your connection, then try again.
        </Notice>
        <div>
          <Button type="button" onClick={retry}>
            Try again
          </Button>
        </div>
      </>
    );

  const { site, policies, deploys, nextCursor } = state;

  const loadOlder = async () => {
    if (!nextCursor) return;
    const started = generation.current;
    setMore('loading');
    const { data } = await api
      .GET('/api/v0/sites/{site}/deploys', {
        params: { path: { site: site.name }, query: { cursor: nextCursor } },
      })
      .catch(() => ({ data: undefined }));
    if (started !== generation.current) return;
    if (!data) return setMore('error');
    setMore('idle');
    setState((current) =>
      current.kind === 'ready'
        ? {
            ...current,
            deploys: [...current.deploys, ...data.items],
            nextCursor: data.nextCursor,
          }
        : current,
    );
  };

  /** Reads the site again without leaving the page; a failure says so inline. */
  const refresh = (after: 'deploys' | 'access' = 'deploys') => {
    const started = ++generation.current;
    setMore('idle');
    setRefreshFailed(null);
    loadSite(api, name).then(
      (loaded) => {
        if (started === generation.current)
          setState({ kind: 'ready', ...loaded });
      },
      () => {
        if (started === generation.current) setRefreshFailed(after);
      },
    );
  };

  // A rollback or a promoted preview: either way one deploy went live.
  const afterActivation = ({ site: now, deploy }: Activation) => {
    // Show the answer straight away, then read every status again in place.
    setState((current) =>
      current.kind === 'ready'
        ? {
            ...current,
            site: now,
            deploys: current.deploys.map((d) =>
              d.id === deploy.id
                ? deploy
                : d.status === 'active'
                  ? { ...d, status: 'inactive' }
                  : d,
            ),
          }
        : current,
    );
    refresh();
  };

  const accessRefreshFailed = refreshFailed === 'access' && (
    <div className="sites-error">
      <Notice tone="danger">
        Saved, but could not refresh the site details. The access shown here may
        be out of date.
      </Notice>
      <Button type="button" onClick={() => refresh('access')}>
        Refresh
      </Button>
    </div>
  );

  let body;
  switch (tab) {
    case 'overview':
      body = (
        <OverviewTab
          api={api}
          site={site}
          policies={policies}
          deploys={deploys}
          linksVersion={linksVersion}
          refreshNotice={accessRefreshFailed}
        />
      );
      break;
    case 'links':
      body = linkId ? (
        <LinkDetail key={linkId} api={api} name={site.name} publicId={linkId} />
      ) : (
        <ShareLinksCard key={linksVersion} api={api} site={site} />
      );
      break;
    case 'access':
      body = (
        <>
          {accessRefreshFailed}
          <AccessTab api={api} site={site} onChange={() => refresh('access')} />
        </>
      );
      break;
    case 'domains':
      body = <DomainsCard api={api} site={site} onSiteChange={setSite} />;
      break;
    case 'deploys':
      body = (
        <DeploysTab
          api={api}
          site={site}
          deploys={deploys}
          nextCursor={nextCursor}
          more={more}
          onOlder={loadOlder}
          refreshFailed={refreshFailed === 'deploys'}
          onRefresh={() => refresh()}
          onRolledBack={afterActivation}
        />
      );
      break;
    case 'previews':
      body = (
        <PreviewsTab
          api={api}
          site={site}
          policy={defaultPolicy(policies)}
          deploys={deploys}
          onLinkCreated={() => setLinksVersion((n) => n + 1)}
          onPromoted={afterActivation}
        />
      );
      break;
    case 'access-log':
      body = <SiteAccessLogTab api={api} name={site.name} />;
      break;
    case 'settings':
      body = <SettingsTab api={api} site={site} organise={organise} />;
      break;
  }

  return (
    <>
      <p className="site-back">
        <a href="/sites">All sites</a>
      </p>
      <SiteHeader
        api={api}
        site={site}
        policy={defaultPolicy(policies)}
        organise={organise}
        onLinkCreated={() => setLinksVersion((n) => n + 1)}
        compact={!wide && tab !== 'overview'}
      />
      <SiteTabs name={site.name} tab={tab} />
      <div className="site-tab">{body}</div>
    </>
  );
}
