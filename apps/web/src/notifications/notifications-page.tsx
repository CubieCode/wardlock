// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient, components } from '@wardlock/apiclient-ts';
import { Card, Checkbox, Notice, PageHeader, Toast } from '@wardlock/ui';
import { useEffect, useState } from 'react';
import { failure, unreachable } from '../auth/messages.js';
import { useTitle } from '../shell/page.js';

type Settings = components['schemas']['NotificationSettings'];
type Setting = keyof Settings;

type State =
  | { kind: 'loading' }
  | { kind: 'error' }
  | { kind: 'ready'; settings: Settings };

/** The settings as the page lists them, grouped by what they are about. */
const groups: {
  title: string;
  items: { key: Setting; label: string; hint: string }[];
}[] = [
  {
    title: 'Custom domains',
    items: [
      {
        key: 'domainVerified',
        label: 'A domain is verified',
        hint: 'When a custom domain passes its DNS check and starts serving your site.',
      },
      {
        key: 'domainFailing',
        label: 'A domain is failing',
        hint: 'When a custom domain has failed its checks for 24 hours, and again if it stops being served.',
      },
    ],
  },
  {
    title: 'Share links',
    items: [
      {
        key: 'linkUsedUp',
        label: 'A share link is used up',
        hint: 'When a link with a use limit has no uses left.',
      },
      {
        key: 'linksExpiring',
        label: 'Share links expire soon',
        hint: 'Links expiring within 48 hours, in at most one email a day.',
      },
    ],
  },
  {
    title: 'API tokens and previews',
    items: [
      {
        key: 'tokenExpiring',
        label: 'An API token expires soon',
        hint: '7 days before a token stops working.',
      },
      {
        key: 'previewExpiring',
        label: 'A preview expires soon',
        hint: '2 days before a preview stops being served.',
      },
    ],
  },
];

const labelOf = (key: Setting) =>
  groups.flatMap((g) => g.items).find((item) => item.key === key)!.label;

/**
 * `/notifications`: which emails the signed-in owner gets. Each box saves
 * alone as it changes, so there is nothing to submit and two tabs cannot
 * overwrite each other's other boxes.
 */
export function NotificationsPage({ api }: { api: ApiClient }) {
  useTitle('Notifications');
  const [state, setState] = useState<State>({ kind: 'loading' });
  const [saved, setSaved] = useState<{ text: string; key: number } | null>(
    null,
  );
  const [problem, setProblem] = useState<string | null>(null);

  useEffect(() => {
    let live = true;
    api
      .GET('/api/v0/notification-settings')
      .then(({ data }) => {
        if (live)
          setState(
            data ? { kind: 'ready', settings: data } : { kind: 'error' },
          );
      })
      .catch(() => {
        if (live) setState({ kind: 'error' });
      });
    return () => {
      live = false;
    };
  }, [api]);

  const show = (key: Setting, value: boolean) =>
    setState((current) =>
      current.kind === 'ready'
        ? { ...current, settings: { ...current.settings, [key]: value } }
        : current,
    );

  const change = async (key: Setting, value: boolean) => {
    show(key, value);
    setProblem(null);
    const refused = (reason: string) => {
      show(key, !value);
      setProblem(`“${labelOf(key)}” was not changed. ${reason}`);
    };
    try {
      const { data, response } = await api.PATCH(
        '/api/v0/notification-settings',
        { body: { [key]: value } },
      );
      if (data)
        setSaved((last) => ({
          text: value ? 'Email turned on' : 'Email turned off',
          key: (last?.key ?? 0) + 1,
        }));
      else refused(failure(response));
    } catch {
      refused(unreachable);
    }
  };

  return (
    <>
      <PageHeader
        title="Notifications"
        description="Choose which emails Wardlock sends you, at the address you sign in with."
      />
      {problem ? <Notice tone="danger">{problem}</Notice> : null}
      {state.kind === 'loading' && <p role="status">Loading your settings…</p>}
      {state.kind === 'error' && (
        <Notice tone="danger">
          Could not load your settings. Reload the page to try again.
        </Notice>
      )}
      {state.kind === 'ready' && (
        <div className="notification-groups">
          {groups.map((group) => (
            <Card key={group.title} title={group.title}>
              {group.items.map((item) => (
                <Checkbox
                  key={item.key}
                  label={item.label}
                  hint={item.hint}
                  checked={state.settings[item.key]}
                  onChange={(event) =>
                    void change(item.key, event.target.checked)
                  }
                />
              ))}
            </Card>
          ))}
          <Card title="Always sent">
            <Checkbox
              label="A file is withheld"
              hint="When content screening withholds a file on one of your sites. This email cannot be turned off."
              checked
              disabled
              readOnly
            />
          </Card>
        </div>
      )}
      <Toast
        message={saved?.text ?? null}
        toastKey={saved?.key ?? 0}
        onDismiss={() => setSaved(null)}
      />
    </>
  );
}
