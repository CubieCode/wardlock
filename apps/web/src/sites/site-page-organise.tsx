// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient, components } from '@wardlock/apiclient-ts';
import {
  Button,
  Menu,
  MenuCheckboxItem,
  MenuItem,
  Popover,
  PopoverFilter,
  TagChip,
  type TagSlot,
} from '@wardlock/ui';
import { useEffect, useState } from 'react';
import type { Site } from './format.js';

export type Group = components['schemas']['Group'];
export type Tag = components['schemas']['Tag'];

export type OrganisationState =
  | { kind: 'loading' }
  | { kind: 'error' }
  | { kind: 'ready'; groups: Group[]; tags: Tag[] };

/** Tag names: 1 to 30 lowercase letters, digits and hyphens (SPEC). */
const tagName = /^[a-z0-9-]{1,30}$/;

async function loadOrganisation(api: ApiClient) {
  const [groups, tags] = await Promise.all([
    api.GET('/api/v0/groups'),
    api.GET('/api/v0/tags'),
  ]);
  if (!groups.data || !tags.data)
    throw new Error('could not load groups and tags');
  return { groups: groups.data.items, tags: tags.data.items };
}

export interface Organise {
  state: OrganisationState;
  /** True while a change is on its way; further changes wait for it. */
  busy: boolean;
  /** What the last change could not do, in words. */
  error: string | null;
  reload: () => void;
  togglePin: () => void;
  setGroup: (groupId: string | undefined) => void;
  setTags: (tagIds: string[]) => void;
  /** Creates a tag with this name and gives it to the site. */
  createTag: (name: string) => void;
}

const failure = (what: string, message: string | undefined) =>
  message
    ? `Could not ${what}: ${message}`
    : `Could not ${what}. Check your connection, then try again.`;

/**
 * The owner's groups and tags, and the site's pin, group and tags changes.
 * Each change answers the site as it is now, which `onSite` receives.
 */
export function useOrganise(
  api: ApiClient,
  site: Site,
  onSite: (site: Site) => void,
): Organise {
  const [state, setState] = useState<OrganisationState>({ kind: 'loading' });
  const [attempt, setAttempt] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let live = true;
    loadOrganisation(api).then(
      (loaded) => live && setState({ kind: 'ready', ...loaded }),
      () => live && setState({ kind: 'error' }),
    );
    return () => {
      live = false;
    };
  }, [api, attempt]);

  const path = { site: site.name };

  /** Runs one change at a time and reports its failure. */
  const run = async (what: string, change: () => Promise<Site | string>) => {
    if (busy) return;
    setBusy(true);
    setError(null);
    const result = await change().catch(() => undefined);
    setBusy(false);
    if (typeof result === 'object') onSite(result);
    else setError(failure(what, result));
  };

  const organise = (
    what: string,
    groupId: string | undefined,
    tagIds: string[],
  ) =>
    run(what, async () => {
      const { data, error } = await api.PUT(
        '/api/v0/sites/{site}/organisation',
        {
          params: { path },
          body: groupId ? { groupId, tagIds } : { tagIds },
        },
      );
      return data ?? error?.message ?? '';
    });

  return {
    state,
    busy,
    error,
    reload: () => {
      setState({ kind: 'loading' });
      setAttempt((n) => n + 1);
    },
    togglePin: () =>
      run(site.pinned ? 'unpin the site' : 'pin the site', async () => {
        const { data, error } = site.pinned
          ? await api.DELETE('/api/v0/sites/{site}/pin', { params: { path } })
          : await api.PUT('/api/v0/sites/{site}/pin', { params: { path } });
        return data ?? error?.message ?? '';
      }),
    setGroup: (groupId) => organise('change the group', groupId, site.tagIds),
    setTags: (tagIds) => organise('change the tags', site.groupId, tagIds),
    createTag: (name) =>
      run('create the tag', async () => {
        const created = await api.POST('/api/v0/tags', { body: { name } });
        if (!created.data) return created.error?.message ?? '';
        const tag = created.data;
        setState((current) =>
          current.kind === 'ready'
            ? {
                ...current,
                tags: [...current.tags, tag].sort((a, b) =>
                  a.name < b.name ? -1 : 1,
                ),
              }
            : current,
        );
        const { data, error } = await api.PUT(
          '/api/v0/sites/{site}/organisation',
          {
            params: { path },
            body: {
              ...(site.groupId ? { groupId: site.groupId } : {}),
              tagIds: [...site.tagIds, tag.id],
            },
          },
        );
        return data ?? error?.message ?? '';
      }),
  };
}

const slot = (tag: Tag) => tag.colour as TagSlot;

/** The site's group as a chip, or "No group". */
export function GroupChip({ group }: { group: Group | undefined }) {
  if (!group) return <span className="site-muted">No group</span>;
  return (
    <span className="site-group-chip">
      <span className="site-group-ring" aria-hidden="true" />
      <span className="wl-visually-hidden">Group: </span>
      {group.name}
    </span>
  );
}

/** The site's tags as chips, each with its remove button. */
export function TagChips({
  site,
  tags,
  organise,
}: {
  site: Site;
  tags: Tag[];
  organise: Organise;
}) {
  return site.tagIds.map((id) => {
    const tag = tags.find((t) => t.id === id);
    return tag ? (
      <TagChip
        key={id}
        label={tag.name}
        slot={slot(tag)}
        onRemove={() =>
          organise.setTags(site.tagIds.filter((other) => other !== id))
        }
      />
    ) : null;
  });
}

/**
 * "+ Tag": a popover listing the owner's tags with the site's checked, a
 * filter field, and Create tag for a new valid name.
 */
export function TagPicker({
  site,
  tags,
  organise,
}: {
  site: Site;
  tags: Tag[];
  organise: Organise;
}) {
  const [open, setOpen] = useState(false);
  const [filter, setFilter] = useState('');
  const query = filter.trim().toLowerCase();
  const shown = tags.filter((tag) => tag.name.includes(query));
  const exists = tags.some((tag) => tag.name === query);
  const canCreate = query !== '' && !exists && tagName.test(query);
  const has = new Set(site.tagIds);

  const toggle = (tag: Tag) =>
    organise.setTags(
      has.has(tag.id)
        ? site.tagIds.filter((id) => id !== tag.id)
        : [...site.tagIds, tag.id],
    );

  return (
    <Popover
      label="Tags"
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (!next) setFilter('');
      }}
      trigger={(props) => (
        <button {...props} type="button" className="site-add-tag">
          <span aria-hidden="true">+</span>Tag
        </button>
      )}
    >
      <PopoverFilter
        label="Find or create a tag"
        placeholder="Tag name"
        value={filter}
        onChange={setFilter}
      />
      {shown.length > 0 || canCreate ? (
        <Menu label="Tags">
          {shown.map((tag) => (
            <MenuCheckboxItem
              key={tag.id}
              checked={has.has(tag.id)}
              onSelect={() => toggle(tag)}
            >
              <span
                className="wl-tag-dot"
                style={{ background: `var(--color-tag-${slot(tag)})` }}
                aria-hidden="true"
              />{' '}
              {tag.name}
            </MenuCheckboxItem>
          ))}
          {canCreate ? (
            <MenuItem
              icon="+"
              onSelect={() => {
                organise.createTag(query);
                setFilter('');
              }}
            >
              Create tag “{query}”
            </MenuItem>
          ) : null}
        </Menu>
      ) : (
        <p className="site-tag-hint">
          {query
            ? 'Tag names use lowercase letters, digits and hyphens, up to 30 characters.'
            : 'No tags yet. Type a name to create one.'}
        </p>
      )}
    </Popover>
  );
}

/** The group and tag chips with "+ Tag", or why they could not load. */
export function Organisation({
  site,
  organise,
}: {
  site: Site;
  organise: Organise;
}) {
  const { state } = organise;
  if (state.kind === 'loading')
    return <p className="site-muted">Loading groups and tags…</p>;
  if (state.kind === 'error')
    return (
      <div className="site-organisation">
        <span className="site-muted">Could not load groups and tags.</span>
        <Button type="button" onClick={organise.reload}>
          Reload groups and tags
        </Button>
      </div>
    );
  return (
    <div
      className="site-organisation"
      role="group"
      aria-label="Group and tags"
      aria-busy={organise.busy}
    >
      <GroupChip group={state.groups.find((g) => g.id === site.groupId)} />
      <TagChips site={site} tags={state.tags} organise={organise} />
      <TagPicker site={site} tags={state.tags} organise={organise} />
    </div>
  );
}
