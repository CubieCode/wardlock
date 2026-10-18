// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient } from '@wardlock/apiclient-ts';
import { Button, Card, EmptyState, Notice } from '@wardlock/ui';
import { useEffect, useRef, useState } from 'react';
import type { Site } from '../sites/format.js';
import { RotateLinkDialog, RevokeLinkDialog } from './confirm.js';
import { CreateLinkDialog } from './create.js';
import {
  expiryText,
  lastOpenedText,
  LinkStatus,
  linkName,
  scopeText,
  usesText,
  type CreatedShareLink,
  type ShareLink,
} from './format.js';
import { RevealDialog } from './reveal.js';
import './links.css';

type ListState =
  | { kind: 'loading' }
  | { kind: 'error' }
  | { kind: 'ready'; links: ShareLink[]; nextCursor: string | undefined };

type Open =
  | { kind: 'none' }
  | { kind: 'create' }
  | { kind: 'revoke' | 'rotate'; link: ShareLink }
  // The one-time response: held only while its dialog is open.
  | { kind: 'reveal'; created: CreatedShareLink; rotated: boolean };

/**
 * A site's share links: create one (its URL shown once), list them with
 * what each grants, and revoke or rotate them. Each row leads to the link's
 * access log.
 */
export function ShareLinksCard({ api, site }: { api: ApiClient; site: Site }) {
  const [state, setState] = useState<ListState>({ kind: 'loading' });
  const [open, setOpen] = useState<Open>({ kind: 'none' });
  const [done, setDone] = useState<string | null>(null);
  const [more, setMore] = useState<'idle' | 'loading' | 'error'>('idle');
  const [attempt, setAttempt] = useState(0);
  // Bumped by every change, so an older page requested before it cannot
  // put back a revoked link.
  const generation = useRef(0);

  useEffect(() => {
    let live = true;
    api
      .GET('/api/v0/sites/{site}/links', {
        params: { path: { site: site.name } },
      })
      .then(
        ({ data }) =>
          live &&
          setState(
            data
              ? {
                  kind: 'ready',
                  links: data.items,
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

  const update = (change: (links: ShareLink[]) => ShareLink[]) => {
    generation.current += 1;
    setMore('idle');
    setState((current) =>
      current.kind === 'ready'
        ? { ...current, links: change(current.links) }
        : current,
    );
  };

  const loadOlder = async () => {
    if (state.kind !== 'ready' || !state.nextCursor) return;
    const started = generation.current;
    setMore('loading');
    const { data } = await api
      .GET('/api/v0/sites/{site}/links', {
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
            links: [
              ...current.links,
              ...data.items.filter(
                (l) => !current.links.some((c) => c.id === l.id),
              ),
            ],
            nextCursor: data.nextCursor,
          }
        : current,
    );
  };

  const close = () => setOpen({ kind: 'none' });

  let body;
  if (state.kind === 'loading')
    body = <p role="status">Loading share links…</p>;
  else if (state.kind === 'error')
    body = (
      <div className="sites-error">
        <Notice tone="danger">
          Could not load the share links. Check your connection, then try again.
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
  else if (state.links.length === 0)
    body = (
      <EmptyState title="No share links yet">
        <p>
          A share link lets anyone who has it open this site, or one folder of
          it, without signing in. Give each one a label so you know who has it.
        </p>
      </EmptyState>
    );
  else
    body = (
      <ul className="link-list" aria-label="Share links">
        {state.links.map((link) => (
          <LinkRow
            key={link.id}
            site={site}
            link={link}
            onRevoke={() => setOpen({ kind: 'revoke', link })}
            onRotate={() => setOpen({ kind: 'rotate', link })}
          />
        ))}
      </ul>
    );

  return (
    <Card title="Share links">
      <div className="link-intro">
        <p>
          Links open the site directly, with no login page. Revoked links stop
          working at once.
        </p>
        <Button
          type="button"
          variant="primary"
          onClick={() => {
            setDone(null);
            setOpen({ kind: 'create' });
          }}
        >
          Create link
        </Button>
      </div>
      {done && <Notice>{done}</Notice>}
      {body}
      {more === 'error' && (
        <Notice tone="danger">Could not load more links. Try again.</Notice>
      )}
      {state.kind === 'ready' && state.nextCursor ? (
        <div>
          <Button
            type="button"
            disabled={more === 'loading'}
            onClick={loadOlder}
          >
            {more === 'loading' ? 'Loading…' : 'Show older links'}
          </Button>
        </div>
      ) : null}

      {open.kind === 'create' && (
        <CreateLinkDialog
          api={api}
          site={site}
          onClose={close}
          onCreated={(created) => {
            update((links) => [created.shareLink, ...links]);
            setOpen({ kind: 'reveal', created, rotated: false });
          }}
        />
      )}
      {open.kind === 'revoke' && (
        <RevokeLinkDialog
          api={api}
          site={site}
          link={open.link}
          onClose={close}
          onRevoked={() => {
            update((links) => links.filter((l) => l.id !== open.link.id));
            setDone(`Revoked ${linkName(open.link)}. It no longer works.`);
            close();
          }}
        />
      )}
      {open.kind === 'rotate' && (
        <RotateLinkDialog
          api={api}
          site={site}
          link={open.link}
          onClose={close}
          onRotated={(created) => {
            update((links) =>
              links.map((l) =>
                l.id === created.shareLink.id ? created.shareLink : l,
              ),
            );
            setOpen({ kind: 'reveal', created, rotated: true });
          }}
        />
      )}
      {open.kind === 'reveal' && (
        <RevealDialog
          created={open.created}
          rotated={open.rotated}
          onClose={close}
        />
      )}
    </Card>
  );
}

/** One link: its status, label, ID, what it grants and its actions. */
function LinkRow({
  site,
  link,
  onRevoke,
  onRotate,
}: {
  site: Site;
  link: ShareLink;
  onRevoke: () => void;
  onRotate: () => void;
}) {
  const name = linkName(link);
  return (
    <li className="link-row">
      <div className="link-row-main">
        <p className="link-row-title">
          <LinkStatus link={link} />
          <span className={link.label ? undefined : 'link-muted'}>
            {link.label ?? 'No label'}
          </span>
          <code className="link-id">{link.publicId}</code>
        </p>
        <p className="link-row-meta">
          {scopeText(link.path)} · {expiryText(link)} · {usesText(link)} ·{' '}
          {lastOpenedText(link)}
        </p>
      </div>
      <div className="link-row-actions">
        <a
          className="wl-button wl-button-secondary link-action"
          href={`/sites/${encodeURIComponent(site.name)}/links/${link.publicId}`}
          aria-label={`Access log of ${name}`}
        >
          Access log
        </a>
        <Button type="button" aria-label={`Rotate ${name}`} onClick={onRotate}>
          Rotate
        </Button>
        <Button type="button" aria-label={`Revoke ${name}`} onClick={onRevoke}>
          Revoke
        </Button>
      </div>
    </li>
  );
}
