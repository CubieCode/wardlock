// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient, components } from '@wardlock/apiclient-ts';
import { Button, Notice, PageHeader } from '@wardlock/ui';
import { useEffect, useState } from 'react';
import { useTitle } from '../shell/page.js';
import { GroupsCard, type Group } from './groups.js';
import { TagsCard, type Tag } from './tags.js';
import './organise.css';

interface Organisation {
  groups: Group[];
  tags: Tag[];
  /** Sites in each group and with each tag, by ID. */
  groupSites: Record<string, number>;
  tagSites: Record<string, number>;
}

type State =
  { kind: 'loading' } | { kind: 'error' } | ({ kind: 'ready' } & Organisation);

type SiteCounts = components['schemas']['SiteCounts'];

/** The counts by group and tag ID. */
function countsByID(counts: SiteCounts) {
  return {
    groupSites: Object.fromEntries(
      counts.groups.map((row) => [row.groupId, row.sites]),
    ),
    tagSites: Object.fromEntries(
      counts.tags.map((row) => [row.tagId, row.sites]),
    ),
  };
}

async function load(api: ApiClient): Promise<Organisation> {
  const [groups, tags, counts] = await Promise.all([
    api.GET('/api/v0/groups'),
    api.GET('/api/v0/tags'),
    api.GET('/api/v0/site-counts'),
  ]);
  if (!groups.data || !tags.data || !counts.data)
    throw new Error('could not load groups and tags');
  return {
    groups: groups.data.items,
    tags: tags.data.items,
    ...countsByID(counts.data),
  };
}

// The server lists groups by name ignoring case, and tags by name.
const byGroupName = (a: Group, b: Group) =>
  a.name.toLowerCase() < b.name.toLowerCase() ? -1 : 1;
const byTagName = (a: Tag, b: Tag) => (a.name < b.name ? -1 : 1);

/** Adds or replaces one item, keeping the server's order. */
function put<T extends { id: string }>(
  items: T[],
  item: T,
  order: (a: T, b: T) => number,
) {
  return [...items.filter((other) => other.id !== item.id), item].sort(order);
}

const without = <T extends { id: string }>(items: T[], id: string) =>
  items.filter((item) => item.id !== id);

/**
 * `/organise`: create, rename and delete the owner's groups and tags, and
 * choose each tag's colour. Each count links to the sites list filtered to
 * that group or tag. Changes answer the group or tag as it is now, so the
 * lists are updated in place rather than fetched again.
 */
export function OrganisePage({ api }: { api: ApiClient }) {
  useTitle('Groups and tags');
  const [state, setState] = useState<State>({ kind: 'loading' });
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    let live = true;
    load(api).then(
      (loaded) => live && setState({ kind: 'ready', ...loaded }),
      () => live && setState({ kind: 'error' }),
    );
    return () => {
      live = false;
    };
  }, [api, attempt]);

  const update = (change: (current: Organisation) => Partial<Organisation>) =>
    setState((current) =>
      current.kind === 'ready' ? { ...current, ...change(current) } : current,
    );

  // Before a delete is confirmed, so it names how many sites it holds now.
  const recount = async () => {
    const counts = await api.GET('/api/v0/site-counts').catch(() => undefined);
    if (counts?.data) update(() => countsByID(counts.data));
  };

  return (
    <>
      <PageHeader
        title="Groups and tags"
        description="Sort your sites with groups and tags. Only you see them; visitors never do."
      />
      {state.kind === 'loading' ? (
        <p role="status">Loading groups and tags…</p>
      ) : null}
      {state.kind === 'error' ? (
        <div className="organise-failed">
          <Notice tone="danger">
            Could not load your groups and tags. Check your connection, then try
            again.
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
        </div>
      ) : null}
      {state.kind === 'ready' ? (
        <div className="organise-columns">
          <GroupsCard
            api={api}
            groups={state.groups}
            counts={state.groupSites}
            recount={recount}
            onCreated={(group) =>
              update(({ groups }) => ({
                groups: put(groups, group, byGroupName),
              }))
            }
            onRenamed={(group) =>
              update(({ groups }) => ({
                groups: put(groups, group, byGroupName),
              }))
            }
            onDeleted={(id) =>
              update(({ groups }) => ({ groups: without(groups, id) }))
            }
          />
          <TagsCard
            api={api}
            tags={state.tags}
            counts={state.tagSites}
            recount={recount}
            onCreated={(tag) =>
              update(({ tags }) => ({ tags: put(tags, tag, byTagName) }))
            }
            onChanged={(tag) =>
              update(({ tags }) => ({ tags: put(tags, tag, byTagName) }))
            }
            onDeleted={(id) =>
              update(({ tags }) => ({ tags: without(tags, id) }))
            }
          />
        </div>
      ) : null}
    </>
  );
}
