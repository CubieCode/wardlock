// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only
import type { ApiClient, Health } from '@wardlock/apiclient-ts';
import { Card, Notice, PageHeader, StatusBadge } from '@wardlock/ui';
import { useEffect, useState } from 'react';
import { useTitle } from '../shell/page.js';

type ServerState =
  { kind: 'loading' } | { kind: 'ready'; health: Health } | { kind: 'error' };

/** `/`: the signed-in home page. Shows the server this dashboard talks to. */
export function OverviewPage({ api }: { api: ApiClient }) {
  const [server, setServer] = useState<ServerState>({ kind: 'loading' });
  useTitle('Overview');

  useEffect(() => {
    let live = true;
    api
      .GET('/api/v0/health')
      .then(({ data }) => {
        if (live)
          setServer(data ? { kind: 'ready', health: data } : { kind: 'error' });
      })
      .catch(() => {
        if (live) setServer({ kind: 'error' });
      });
    return () => {
      live = false;
    };
  }, [api]);

  return (
    <>
      <PageHeader
        title="Overview"
        description="The Wardlock server this dashboard is connected to."
      />
      <Card title="Server">
        {server.kind === 'loading' && (
          <StatusBadge tone="neutral">Checking…</StatusBadge>
        )}
        {server.kind === 'ready' && (
          <>
            <StatusBadge tone="success">Connected</StatusBadge>
            <p>
              Version <code>{server.health.version}</code>
            </p>
          </>
        )}
        {server.kind === 'error' && (
          <Notice tone="danger">
            Could not reach the server. Check that wardlockd is running, then
            reload this page.
          </Notice>
        )}
      </Card>
    </>
  );
}
