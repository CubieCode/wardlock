// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient } from '@wardlock/apiclient-ts';
import {
  Button,
  Card,
  CopyField,
  EmptyState,
  Notice,
  PageHeader,
  StatusBadge,
} from '@wardlock/ui';
import { useEffect, useRef, useState } from 'react';
import { DomainsCard } from '../domains/domains.js';
import { ShareLinksCard } from '../links/share-links.js';
import { PoliciesCard } from '../policies/policies.js';
import { useTitle } from '../shell/page.js';
import {
  defaultPolicy,
  fileCount,
  formatBytes,
  PolicyBadge,
  shortId,
  Time,
  type Activation,
  type Deploy,
  type Policy,
  type Site,
} from './format.js';
import { SiteHeading } from './open-site.js';
import { RollbackDialog } from './rollback.js';
import { TeardownDialog } from './teardown.js';
import './sites.css';

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

/** `/sites/:name`: one site's address, access, deploy history and teardown. */
export function SitePage({ api, name }: { api: ApiClient; name: string }) {
  const [state, setState] = useState<PageState>({ kind: 'loading' });
  const [rollbackTo, setRollbackTo] = useState<Deploy | null>(null);
  const [tearingDown, setTearingDown] = useState(false);
  const [rolledBack, setRolledBack] = useState<Deploy | null>(null);
  // Bumped when Open site creates a share link, so the list reloads with it.
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
  useTitle(state.kind === 'not-found' ? 'Site not found' : name);

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
  const policy = defaultPolicy(policies);
  const pathPolicies = policies.filter((p) => p.path !== '/');
  const active = deploys.find((d) => d.id === site.activeDeployId);

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

  const afterRollback = ({ site: now, deploy }: Activation) => {
    setRollbackTo(null);
    setRolledBack(deploy);
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

  return (
    <>
      <p className="site-back">
        <a href="/sites">All sites</a>
      </p>
      <SiteHeading
        api={api}
        site={site}
        policy={policy}
        description={
          <>
            Created <Time at={site.createdAt} />
          </>
        }
        onLinkCreated={() => setLinksVersion((n) => n + 1)}
      />

      <Card title="Address and access">
        <CopyField label="Site address" value={site.url} />
        <div className="site-access">
          <PolicyBadge policy={policy} />
          <p>{accessSummary(policy)}</p>
        </div>
        {pathPolicies.length > 0 && (
          <>
            <h3 className="site-subheading">Paths with their own access</h3>
            <ul className="site-paths">
              {pathPolicies.map((p) => (
                <li key={p.id}>
                  <code>{p.path}</code>
                  <PolicyBadge policy={p} />
                </li>
              ))}
            </ul>
          </>
        )}
        {refreshFailed === 'access' && (
          <div className="sites-error">
            <Notice tone="danger">
              Saved, but could not refresh the site details. The access shown
              here may be out of date.
            </Notice>
            <Button type="button" onClick={() => refresh('access')}>
              Refresh
            </Button>
          </div>
        )}
      </Card>

      <PoliciesCard api={api} site={site} onChange={() => refresh('access')} />
      <ShareLinksCard key={linksVersion} api={api} site={site} />
      <DomainsCard api={api} site={site} />

      <Card title="Deploys">
        {rolledBack && (
          <Notice>
            Rolled back. Deploy <code>{shortId(rolledBack.id)}</code> is live.
          </Notice>
        )}
        {refreshFailed === 'deploys' && (
          <div className="sites-error">
            <Notice tone="danger">
              Could not refresh the deploy history. The statuses below may be
              out of date.
            </Notice>
            <Button type="button" onClick={() => refresh()}>
              Refresh
            </Button>
          </div>
        )}
        {deploys.length === 0 ? (
          <EmptyState title="No deploys yet">
            <p>Each deploy of this site is listed here, newest first.</p>
          </EmptyState>
        ) : (
          <ul className="deploy-list" aria-label="Deploy history">
            {deploys.map((deploy) => (
              <DeployRow
                key={deploy.id}
                deploy={deploy}
                onRollback={
                  site.activeDeployId ? () => setRollbackTo(deploy) : undefined
                }
              />
            ))}
          </ul>
        )}
        {more === 'error' && (
          <Notice tone="danger">
            Could not load older deploys. Try again.
          </Notice>
        )}
        {nextCursor ? (
          <div>
            <Button
              type="button"
              disabled={more === 'loading'}
              onClick={loadOlder}
            >
              {more === 'loading' ? 'Loading…' : 'Show older deploys'}
            </Button>
          </div>
        ) : null}
      </Card>

      <Card title="Tear down this site">
        <p>
          Deletes the site with its deploys and access settings, stops serving{' '}
          <code>{site.hostname}</code> and frees the name{' '}
          <strong>{site.name}</strong>.
        </p>
        <div>
          <Button type="button" onClick={() => setTearingDown(true)}>
            Tear down site
          </Button>
        </div>
      </Card>

      {rollbackTo && (
        <RollbackDialog
          api={api}
          site={site}
          live={active}
          to={rollbackTo}
          onClose={() => setRollbackTo(null)}
          onDone={afterRollback}
        />
      )}
      <TeardownDialog
        api={api}
        site={site}
        open={tearingDown}
        onClose={() => setTearingDown(false)}
      />
    </>
  );
}

function accessSummary(policy: Policy | undefined) {
  if (!policy) return 'Could not read who can open this site.';
  if (policy.visibility === 'public')
    return 'Anyone with the address can open this site.';
  return 'Only visitors with access can open this site.';
}

/** One deploy: its status word, ID, message, time, size and rollback. */
function DeployRow({
  deploy,
  onRollback,
}: {
  deploy: Deploy;
  /** Absent when no deploy is live, so there is nothing to roll back from. */
  onRollback: (() => void) | undefined;
}) {
  const id = shortId(deploy.id);
  return (
    <li className="deploy-row">
      <div className="deploy-row-status">
        {deploy.status === 'active' && (
          <StatusBadge tone="success">Live</StatusBadge>
        )}
        {deploy.status === 'inactive' && (
          <StatusBadge tone="neutral">Not live</StatusBadge>
        )}
        {deploy.status === 'pending' && (
          <StatusBadge tone="neutral">Not activated</StatusBadge>
        )}
      </div>
      <div className="deploy-row-main">
        <p className="deploy-row-title">
          <code>{id}</code>
          <span className={deploy.message ? undefined : 'deploy-row-muted'}>
            {deploy.message || 'No message'}
          </span>
        </p>
        <p className="deploy-row-meta">
          {deploy.activatedAt !== undefined ? (
            <>
              {deploy.status === 'active' ? 'Live since' : 'Last live'}{' '}
              <Time at={deploy.activatedAt} />
            </>
          ) : (
            <>
              Uploaded <Time at={deploy.createdAt} />
            </>
          )}
          {' · '}
          {fileCount(deploy.fileCount)}
          {' · '}
          {formatBytes(deploy.totalSize)}
        </p>
      </div>
      {deploy.status === 'inactive' && onRollback ? (
        <Button
          type="button"
          className="deploy-row-action"
          aria-label={`Roll back to deploy ${id}`}
          onClick={onRollback}
        >
          Roll back
        </Button>
      ) : null}
    </li>
  );
}
