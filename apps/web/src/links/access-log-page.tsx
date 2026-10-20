// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient } from '@wardlock/apiclient-ts';
import { Button, Notice, PageHeader, StatusBadge } from '@wardlock/ui';
import { useEffect, useState } from 'react';
import { useTitle } from '../shell/page.js';
import {
  expiryText,
  lastOpenedText,
  LinkStatus,
  scopeText,
  usesText,
  type ShareLink,
} from './format.js';
import {
  useOlderVisits,
  visitColumns,
  VisitsCard,
  type AccessLogPage,
} from '../sites/visits.js';
import './links.css';

interface Loaded {
  /** Absent when the link is revoked: listings leave revoked links out. */
  link: ShareLink | undefined;
  first: AccessLogPage;
}

type PageState =
  | { kind: 'loading' }
  | { kind: 'not-found' }
  | { kind: 'error' }
  | ({ kind: 'ready' } & Loaded);

class NotFound extends Error {}

/** Pages through the site's links for this one; at most a few requests. */
async function findLink(api: ApiClient, site: string, publicId: string) {
  let cursor: string | undefined;
  for (let page = 0; page < 20; page++) {
    const { data } = await api.GET('/api/v0/sites/{site}/links', {
      params: {
        path: { site },
        query: cursor ? { limit: 100, cursor } : { limit: 100 },
      },
    });
    if (!data) throw new Error('could not list the links');
    const link = data.items.find((l) => l.publicId === publicId);
    if (link || !data.nextCursor) return link;
    cursor = data.nextCursor;
  }
  return undefined;
}

async function load(
  api: ApiClient,
  site: string,
  publicId: string,
): Promise<Loaded> {
  const [log, link] = await Promise.all([
    api.GET('/api/v0/sites/{site}/links/{linkId}/access-log', {
      params: { path: { site, linkId: publicId } },
    }),
    findLink(api, site, publicId).catch(() => undefined),
  ]);
  // A missing link, another owner's and a malformed ID all answer 404 (or
  // 400 for a site name the server refuses to parse).
  if ([404, 400].includes(log.response.status)) throw new NotFound();
  if (!log.data) throw new Error('could not load the access log');
  return { link, first: log.data };
}

const columns = [
  visitColumns.time,
  visitColumns.path,
  visitColumns.result,
  visitColumns.ip,
  visitColumns.browser,
];

/** `/sites/:name/links/:linkId`: who opened one share link, newest first. */
export function LinkAccessLogPage({
  api,
  name,
  publicId,
}: {
  api: ApiClient;
  name: string;
  publicId: string;
}) {
  const [state, setState] = useState<PageState>({ kind: 'loading' });
  const [attempt, setAttempt] = useState(0);
  useTitle(
    state.kind === 'not-found' ? 'Link not found' : `Link access log · ${name}`,
  );

  useEffect(() => {
    let live = true;
    load(api, name, publicId).then(
      (loaded) => live && setState({ kind: 'ready', ...loaded }),
      (error: unknown) =>
        live &&
        setState({ kind: error instanceof NotFound ? 'not-found' : 'error' }),
    );
    return () => {
      live = false;
    };
  }, [api, name, publicId, attempt]);

  const siteHref = `/sites/${encodeURIComponent(name)}`;
  const back = (
    <p className="site-back">
      <a href={siteHref}>Back to {name}</a>
    </p>
  );

  if (state.kind === 'loading') return <p role="status">Loading access log…</p>;
  if (state.kind === 'not-found')
    return (
      <>
        <PageHeader
          title="Link not found"
          description="There is no share link with this ID on this site in your account."
        />
        <p>
          <a href={siteHref}>Go to {name}</a>
        </p>
      </>
    );
  if (state.kind === 'error')
    return (
      <>
        {back}
        <PageHeader title="Link access log" />
        <Notice tone="danger">
          Could not load the access log. Check your connection, then try again.
        </Notice>
        <div>
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
      </>
    );

  const { link, first } = state;
  return (
    <>
      {back}
      <PageHeader
        title={link?.label ?? first.items[0]?.linkLabel ?? `Link ${publicId}`}
        description={
          <>
            Access log of share link <code>{publicId}</code> on {name}, newest
            first.
          </>
        }
      />
      <div className="link-summary">
        {link ? (
          <>
            <LinkStatus link={link} />
            <p>
              {scopeText(link.path)} · {expiryText(link)} · {usesText(link)} ·{' '}
              {lastOpenedText(link)}
            </p>
          </>
        ) : (
          <>
            <StatusBadge tone="neutral">Revoked</StatusBadge>
            <p>This link no longer works. Its past visits are kept below.</p>
          </>
        )}
      </div>

      <LinkVisits api={api} name={name} publicId={publicId} first={first} />
    </>
  );
}

function LinkVisits({
  api,
  name,
  publicId,
  first,
}: {
  api: ApiClient;
  name: string;
  publicId: string;
  first: AccessLogPage;
}) {
  const { entries, nextCursor, more, loadOlder } = useOlderVisits(
    first,
    async (cursor) =>
      (
        await api.GET('/api/v0/sites/{site}/links/{linkId}/access-log', {
          params: { path: { site: name, linkId: publicId }, query: { cursor } },
        })
      ).data,
  );
  return (
    <VisitsCard
      caption={`Visits through link ${publicId}`}
      columns={columns}
      entries={entries}
      nextCursor={nextCursor}
      more={more}
      onOlder={loadOlder}
      empty={
        <p>
          Each request that comes in through this link is listed here, with its
          time, path, IP address and browser.
        </p>
      }
    />
  );
}
