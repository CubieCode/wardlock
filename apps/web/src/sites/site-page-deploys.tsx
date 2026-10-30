// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient } from '@wardlock/apiclient-ts';
import { Button, Card, EmptyState, Notice, StatusTag } from '@wardlock/ui';
import { useState } from 'react';
import {
  fileCount,
  formatBytes,
  shortId,
  Time,
  type Activation,
  type Deploy,
  type Site,
} from './format.js';
import { RollbackDialog } from './rollback.js';
import { siteHref } from './site-page-tabs.js';

/** The Deploys tab: the history, newest first, with rollback. */
export function DeploysTab({
  api,
  site,
  deploys,
  nextCursor,
  more,
  onOlder,
  refreshFailed,
  onRefresh,
  onRolledBack,
}: {
  api: ApiClient;
  site: Site;
  deploys: Deploy[];
  nextCursor: string | undefined;
  more: 'idle' | 'loading' | 'error';
  onOlder: () => void;
  /** True when reading the history again after a rollback failed. */
  refreshFailed: boolean;
  onRefresh: () => void;
  onRolledBack: (activation: Activation) => void;
}) {
  const [rollbackTo, setRollbackTo] = useState<Deploy | null>(null);
  const [rolledBack, setRolledBack] = useState<Deploy | null>(null);
  const active = deploys.find((d) => d.id === site.activeDeployId);

  return (
    <Card title="Deploys">
      <p className="site-card-lead">
        Newest first. Rolling back makes an earlier deploy live at once; a
        preview’s deploy goes live when you promote it on{' '}
        <a href={siteHref(site.name, 'previews')}>Previews</a>.
      </p>
      {rolledBack && (
        <Notice>
          Rolled back. Deploy <code>{shortId(rolledBack.id)}</code> is live.
        </Notice>
      )}
      {refreshFailed && (
        <div className="sites-error">
          <Notice tone="danger">
            Could not refresh the deploy history. The statuses below may be out
            of date.
          </Notice>
          <Button type="button" onClick={onRefresh}>
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
                site.activeDeployId && !site.unavailable
                  ? () => setRollbackTo(deploy)
                  : undefined
              }
            />
          ))}
        </ul>
      )}
      {more === 'error' && (
        <Notice tone="danger">Could not load older deploys. Try again.</Notice>
      )}
      {nextCursor ? (
        <div>
          <Button type="button" disabled={more === 'loading'} onClick={onOlder}>
            {more === 'loading' ? 'Loading…' : 'Show older deploys'}
          </Button>
        </div>
      ) : null}

      {rollbackTo && (
        <RollbackDialog
          api={api}
          site={site}
          live={active}
          to={rollbackTo}
          onClose={() => setRollbackTo(null)}
          onDone={(activation) => {
            setRollbackTo(null);
            setRolledBack(activation.deploy);
            onRolledBack(activation);
          }}
        />
      )}
    </Card>
  );
}

/** A deploy's status as a word. */
export function DeployStatus({ deploy }: { deploy: Deploy }) {
  if (deploy.status === 'active')
    return <StatusTag tone="success">Live</StatusTag>;
  if (deploy.status === 'inactive') return <StatusTag>Not live</StatusTag>;
  if (deploy.status === 'preview')
    return <StatusTag tone="primary">Preview</StatusTag>;
  return <StatusTag>Not activated</StatusTag>;
}

const plural = (count: number, one: string, many: string) =>
  `${new Intl.NumberFormat('en-NZ').format(count)} ${count === 1 ? one : many}`;

/** "2 redirects, 1 header rule"; empty when the deploy has neither. */
export function ruleCounts(deploy: Deploy) {
  const redirects = deploy.redirects?.length ?? 0;
  const headers = deploy.headers?.length ?? 0;
  return [
    ...(redirects ? [plural(redirects, 'redirect', 'redirects')] : []),
    ...(headers ? [plural(headers, 'header rule', 'header rules')] : []),
  ].join(', ');
}

const redirectKinds: Record<number, string> = {
  301: 'permanent',
  302: 'temporary',
  307: 'temporary',
  308: 'permanent',
};

/**
 * A deploy's redirect and header rules, read-only: they come from the
 * `wardlock.json` it was deployed with, so changing them is a new deploy.
 */
export function DeployRules({ deploy }: { deploy: Deploy }) {
  const counts = ruleCounts(deploy);
  if (!counts) return null;
  const id = shortId(deploy.id);
  return (
    <details className="deploy-rules">
      <summary>{counts}</summary>
      <div className="deploy-rules-body">
        <p className="deploy-row-muted">
          From the <code>wardlock.json</code> this deploy was made with. Deploy
          again to change them.
        </p>
        {deploy.redirects?.length ? (
          <>
            <h3 className="deploy-rules-heading">Redirects</h3>
            <p className="deploy-row-muted">
              In order; the first match wins, only where no file is served.
            </p>
            <ol
              className="deploy-rules-list"
              aria-label={`Redirects of deploy ${id}`}
            >
              {deploy.redirects.map((rule, i) => (
                <li key={i} className="deploy-redirect">
                  <code>{rule.from}</code>
                  <span aria-hidden="true">→</span>
                  <span className="wl-visually-hidden">to</span>
                  <code>{rule.to}</code>
                  <span className="deploy-row-muted">
                    {rule.status ?? 301} {redirectKinds[rule.status ?? 301]}
                  </span>
                </li>
              ))}
            </ol>
          </>
        ) : null}
        {deploy.headers?.length ? (
          <>
            <h3 className="deploy-rules-heading">Response headers</h3>
            <p className="deploy-row-muted">
              Every matching rule applies; a later one replaces the same header.
            </p>
            <ol
              className="deploy-rules-list"
              aria-label={`Header rules of deploy ${id}`}
            >
              {deploy.headers.map((rule, i) => (
                <li key={i} className="deploy-header-rule">
                  <code>{rule.path}</code>
                  <dl>
                    {Object.entries(rule.set).map(([name, value]) => (
                      <div key={name}>
                        <dt>{name}</dt>
                        <dd>
                          <code>{value}</code>
                        </dd>
                      </div>
                    ))}
                  </dl>
                </li>
              ))}
            </ol>
          </>
        ) : null}
      </div>
    </details>
  );
}

/** Files, size and the long-term caching option, for one deploy. */
export function deployFacts(deploy: Deploy) {
  return [
    fileCount(deploy.fileCount),
    formatBytes(deploy.totalSize),
    ...(deploy.immutableAssets ? ['Immutable assets'] : []),
  ].join(' · ');
}

/**
 * One deploy: its status word, ID, the preview it was made for, the path a
 * path deploy replaced, message, time, size, its rules and rollback.
 */
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
        <DeployStatus deploy={deploy} />
      </div>
      <div className="deploy-row-main">
        <p className="deploy-row-title">
          <code>{id}</code>
          {deploy.preview !== undefined && (
            <span className="deploy-row-path">
              <span className="deploy-row-muted">Preview</span>{' '}
              <code>{deploy.preview}</code>
            </span>
          )}
          {deploy.path !== undefined && (
            // Only this part of the site changed; the rest is the deploy
            // that was live before.
            <span className="deploy-row-path">
              <span className="deploy-row-muted">Path</span>{' '}
              <code>{deploy.path}</code>
            </span>
          )}
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
          {deployFacts(deploy)}
        </p>
        <DeployRules deploy={deploy} />
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
