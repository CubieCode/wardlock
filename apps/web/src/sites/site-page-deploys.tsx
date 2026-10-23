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
        Newest first. Rolling back makes an earlier deploy live at once.
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
  return <StatusTag>Not activated</StatusTag>;
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
 * One deploy: its status word, ID, the path a path deploy replaced,
 * message, time, size and rollback.
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
