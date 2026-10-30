// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient, components } from '@wardlock/apiclient-ts';
import {
  Button,
  Card,
  CopyField,
  Dialog,
  EmptyState,
  Notice,
  StatusTag,
  type NoticeTone,
} from '@wardlock/ui';
import { useEffect, useRef, useState } from 'react';
import {
  fileCount,
  formatBytes,
  shortId,
  Time,
  type Activation,
  type Deploy,
  type Policy,
  type Site,
} from './format.js';
import { OpenSiteButton } from './open-site.js';
import { DeploySummary } from './rollback.js';
import './previews.css';

export type Preview = components['schemas']['Preview'];

type ListState =
  | { kind: 'loading' }
  | { kind: 'error' }
  | { kind: 'ready'; previews: Preview[]; nextCursor: string | undefined };

type Message = { tone: NoticeTone; text: string } | null;

/** The command the empty state suggests, for this site. */
export const previewCommand = (site: Site) =>
  `wardlock deploy dist ${site.name} --preview pr-42`;

/**
 * The Previews tab: each preview's address, the deploy it serves and when
 * it expires, with Open, Promote and Delete.
 */
export function PreviewsTab({
  api,
  site,
  policy,
  deploys,
  onLinkCreated,
  onPromoted,
}: {
  api: ApiClient;
  site: Site;
  /** The site's `/` policy, which decides how Open works. */
  policy: Policy | undefined;
  /** The loaded history, to show the live deploy in the confirmation. */
  deploys: Deploy[];
  onLinkCreated: () => void;
  onPromoted: (activation: Activation) => void;
}) {
  const [state, setState] = useState<ListState>({ kind: 'loading' });
  const [attempt, setAttempt] = useState(0);
  const [more, setMore] = useState<'idle' | 'loading' | 'error'>('idle');
  const [promoting, setPromoting] = useState<Preview | null>(null);
  const [deleting, setDeleting] = useState<Preview | null>(null);
  const [done, setDone] = useState<Message>(null);
  // The row whose button opened a dialog may be gone afterwards, so focus
  // moves to the result instead of the page body.
  const doneRef = useRef<HTMLDivElement>(null);
  const focusDone = useRef(false);
  // Bumped by every change, so an older page cannot put back a deleted row.
  const generation = useRef(0);

  useEffect(() => {
    let live = true;
    api
      .GET('/api/v0/sites/{site}/previews', {
        params: { path: { site: site.name } },
      })
      .then(
        ({ data }) =>
          live &&
          setState(
            data
              ? {
                  kind: 'ready',
                  previews: data.items,
                  nextCursor: data.nextCursor,
                }
              : { kind: 'error' },
          ),
        () => live && setState({ kind: 'error' }),
      );
    return () => {
      live = false;
    };
  }, [api, site.name, attempt]);

  useEffect(() => {
    if (!focusDone.current) return;
    focusDone.current = false;
    doneRef.current?.focus();
  }, [done]);

  const announce = (message: Message) => {
    setDone(message);
    focusDone.current = true;
  };

  const loadMore = async () => {
    if (state.kind !== 'ready' || !state.nextCursor) return;
    const started = generation.current;
    setMore('loading');
    const { data } = await api
      .GET('/api/v0/sites/{site}/previews', {
        params: {
          path: { site: site.name },
          query: { cursor: state.nextCursor },
        },
      })
      .catch(() => ({ data: undefined }));
    if (started !== generation.current) return;
    if (!data) return setMore('error');
    setMore('idle');
    setState((current) =>
      current.kind === 'ready'
        ? {
            kind: 'ready',
            previews: [
              ...current.previews,
              ...data.items.filter(
                (p) => !current.previews.some((c) => c.name === p.name),
              ),
            ],
            nextCursor: data.nextCursor,
          }
        : current,
    );
  };

  let body;
  if (state.kind === 'loading') body = <p role="status">Loading previews…</p>;
  else if (state.kind === 'error')
    body = (
      <div className="sites-error">
        <Notice tone="danger">
          Could not load the previews. Check your connection, then try again.
        </Notice>
        <Button
          type="button"
          onClick={() => {
            setState({ kind: 'loading' });
            setAttempt((n) => n + 1);
          }}
        >
          Try again
        </Button>
      </div>
    );
  else if (state.previews.length === 0)
    body = (
      <EmptyState title="No previews yet">
        <p>
          Deploy to a preview to check a change at its own address before it
          goes live. Deploying to the same name again replaces it.
        </p>
        <CopyField label="Command" value={previewCommand(site)} />
      </EmptyState>
    );
  else
    body = (
      <ul className="preview-list" aria-label="Previews">
        {state.previews.map((preview) => (
          <PreviewRow
            key={preview.name}
            api={api}
            site={site}
            policy={policy}
            preview={preview}
            onLinkCreated={onLinkCreated}
            onPromote={() => {
              setDone(null);
              setPromoting(preview);
            }}
            onDelete={() => {
              setDone(null);
              setDeleting(preview);
            }}
          />
        ))}
      </ul>
    );

  return (
    <Card title="Previews">
      <p className="site-card-lead">
        Each preview serves its own deploy at its own address, with this site’s
        access settings. Promote one to put its deploy live.
      </p>
      {site.unavailable && (
        <p>
          While the operator has taken this site down, its previews stay as they
          are: none can be opened, promoted or deleted.
        </p>
      )}
      <div
        ref={doneRef}
        role="status"
        tabIndex={-1}
        className="preview-live-notice"
      >
        {done && <Notice tone={done.tone}>{done.text}</Notice>}
      </div>
      {body}
      {more === 'error' && (
        <Notice tone="danger">Could not load more previews. Try again.</Notice>
      )}
      {state.kind === 'ready' && state.nextCursor ? (
        <div>
          <Button
            type="button"
            disabled={more === 'loading'}
            onClick={loadMore}
          >
            {more === 'loading' ? 'Loading…' : 'Show more previews'}
          </Button>
        </div>
      ) : null}

      {promoting && (
        <PromotePreviewDialog
          api={api}
          site={site}
          live={deploys.find((d) => d.id === site.activeDeployId)}
          preview={promoting}
          onClose={() => setPromoting(null)}
          onDone={(activation) => {
            setPromoting(null);
            announce({
              tone: 'info',
              text: `Promoted ${promoting.name}. Deploy ${shortId(activation.deploy.id)} is live.`,
            });
            onPromoted(activation);
          }}
        />
      )}
      {deleting && (
        <DeletePreviewDialog
          api={api}
          site={site}
          preview={deleting}
          onClose={() => setDeleting(null)}
          onDeleted={() => {
            generation.current += 1;
            setMore('idle');
            setState((current) =>
              current.kind === 'ready'
                ? {
                    ...current,
                    previews: current.previews.filter(
                      (p) => p.name !== deleting.name,
                    ),
                  }
                : current,
            );
            setDeleting(null);
            announce({
              tone: 'info',
              text: `Deleted preview ${deleting.name}.`,
            });
          }}
        />
      )}
    </Card>
  );
}

/**
 * One preview: its name, address, the deploy it serves, when that went up
 * and when the preview expires.
 */
function PreviewRow({
  api,
  site,
  policy,
  preview,
  onLinkCreated,
  onPromote,
  onDelete,
}: {
  api: ApiClient;
  site: Site;
  policy: Policy | undefined;
  preview: Preview;
  onLinkCreated: () => void;
  onPromote: () => void;
  onDelete: () => void;
}) {
  const { deploy } = preview;
  const live = deploy.id === site.activeDeployId;
  return (
    <li className="preview-row" aria-label={preview.name}>
      <div className="preview-row-head">
        <p className="preview-row-title">
          <code className="preview-name">{preview.name}</code>
          {live && <StatusTag tone="success">Live on site</StatusTag>}
        </p>
        {!site.unavailable && (
          <div className="preview-row-actions">
            <OpenSiteButton
              api={api}
              site={site}
              policy={policy}
              url={preview.url}
              label={`Open preview ${preview.name}`}
              onLinkCreated={onLinkCreated}
            >
              Open
            </OpenSiteButton>
            {!live && (
              <Button
                type="button"
                aria-label={`Promote ${preview.name}`}
                onClick={onPromote}
              >
                Promote
              </Button>
            )}
            <Button
              type="button"
              aria-label={`Delete preview ${preview.name}`}
              onClick={onDelete}
            >
              Delete
            </Button>
          </div>
        )}
      </div>
      <p className="preview-host">
        <code>{preview.hostname}</code>
      </p>
      <p>
        <code>{shortId(deploy.id)}</code>{' '}
        <span className={deploy.message ? undefined : 'site-muted'}>
          {deploy.message || 'No message'}
        </span>
      </p>
      <p className="preview-row-meta">
        Deployed <Time at={preview.deployedAt} />
        {' · '}
        {fileCount(deploy.fileCount)} · {formatBytes(deploy.totalSize)}
        {' · '}
        {preview.expiresAt === undefined ? (
          'Never expires'
        ) : (
          <>
            Expires <Time at={preview.expiresAt} />
          </>
        )}
      </p>
    </li>
  );
}

/** The server's refusal of a promotion, in plain words. */
function promoteFailure(error: { code: string; message: string } | undefined) {
  if (error?.code === 'stale_base')
    return 'The live site has changed since this path preview was deployed, so promoting it would undo that change. Deploy the preview again, then promote it.';
  if (error?.code === 'not_found')
    return 'This preview no longer exists: it was deleted or has expired.';
  return error?.message
    ? `Could not promote the preview: ${error.message}`
    : 'Could not promote the preview. Check your connection, then try again.';
}

/**
 * Confirms promoting a preview by showing the deploy live now beside the
 * preview's deploy, which replaces it.
 */
export function PromotePreviewDialog({
  api,
  site,
  live,
  preview,
  onClose,
  onDone,
}: {
  api: ApiClient;
  site: Site;
  /** The live deploy, when it is in the loaded part of the history. */
  live: Deploy | undefined;
  preview: Preview;
  onClose: () => void;
  onDone: (activation: Activation) => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const promote = async () => {
    setBusy(true);
    setError(null);
    const result = await api
      .POST('/api/v0/sites/{site}/previews/{preview}/promote', {
        params: { path: { site: site.name, preview: preview.name } },
      })
      .catch(() => null);
    setBusy(false);
    if (result?.data) return onDone(result.data);
    setError(promoteFailure(result?.error));
  };

  return (
    <Dialog
      open
      title={`Promote ${preview.name}?`}
      onClose={() => {
        if (!busy) onClose();
      }}
      actions={
        <>
          <Button type="button" disabled={busy} onClick={onClose}>
            Cancel
          </Button>
          <Button
            type="button"
            variant="primary"
            disabled={busy}
            onClick={promote}
          >
            {busy ? 'Promoting…' : 'Put this deploy live'}
          </Button>
        </>
      }
    >
      <p>
        Visitors to <code>{site.hostname}</code> see deploy{' '}
        <code>{shortId(preview.deploy.id)}</code> straight away. It joins the
        deploy history, so you can roll back, and the preview keeps serving it.
      </p>
      <dl className="rollback-swap">
        <div>
          <dt>Live now</dt>
          <dd>
            {live ? (
              <DeploySummary deploy={live} live />
            ) : site.activeDeployId ? (
              <code>{shortId(site.activeDeployId)}</code>
            ) : (
              <span className="rollback-meta">Nothing is live yet</span>
            )}
          </dd>
        </div>
        <div>
          <dt>Goes live from {preview.name}</dt>
          <dd>
            <DeploySummary deploy={preview.deploy} />
          </dd>
        </div>
      </dl>
      {error && <Notice tone="danger">{error}</Notice>}
    </Dialog>
  );
}

/** Confirms deleting a preview: its address stops answering at once. */
export function DeletePreviewDialog({
  api,
  site,
  preview,
  onClose,
  onDeleted,
}: {
  api: ApiClient;
  site: Site;
  preview: Preview;
  onClose: () => void;
  onDeleted: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const remove = async () => {
    setBusy(true);
    setError(null);
    const result = await api
      .DELETE('/api/v0/sites/{site}/previews/{preview}', {
        params: { path: { site: site.name, preview: preview.name } },
      })
      .catch(() => null);
    setBusy(false);
    // Already gone (deleted elsewhere or expired) is the outcome asked for.
    if (result?.response.ok || result?.response.status === 404)
      return onDeleted();
    setError(
      result?.error?.message
        ? `Could not delete the preview: ${result.error.message}`
        : 'Could not delete the preview. Check your connection, then try again.',
    );
  };

  return (
    <Dialog
      open
      title={`Delete preview ${preview.name}?`}
      onClose={() => {
        if (!busy) onClose();
      }}
      actions={
        <>
          <Button type="button" disabled={busy} onClick={onClose}>
            Cancel
          </Button>
          <Button
            type="button"
            variant="danger"
            disabled={busy}
            onClick={remove}
          >
            {busy ? 'Deleting…' : 'Delete preview'}
          </Button>
        </>
      }
    >
      <p>
        Wardlock stops serving <code>{preview.hostname}</code> at once. The live
        site at <code>{site.hostname}</code> stays as it is, and a deploy that
        went live from this preview stays in its history.
      </p>
      {error && <Notice tone="danger">{error}</Notice>}
    </Dialog>
  );
}
