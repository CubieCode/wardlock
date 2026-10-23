// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient, components } from '@wardlock/apiclient-ts';
import { Button, Card } from '@wardlock/ui';
import { useRef, useState } from 'react';
import { sitesHref } from '../shell/navigator.js';
import { useFocusAfterDelete, useFocusOnReturn } from './focus.js';
import {
  attempt,
  DeleteDialog,
  NameForm,
  remove,
  sites,
  SitesLink,
  type Removal,
} from './shared.js';

export type Group = components['schemas']['Group'];

const nameHint = 'Up to 40 characters, different from your other groups.';

/** The owner's groups, with New group, Rename and Delete. */
export function GroupsCard({
  api,
  groups,
  counts,
  recount,
  onCreated,
  onRenamed,
  onDeleted,
}: {
  api: ApiClient;
  groups: Group[];
  /** Sites in each group, by group ID. */
  counts: Record<string, number>;
  /** Reads the site counts again. */
  recount: () => Promise<void>;
  onCreated: (group: Group) => void;
  onRenamed: (group: Group) => void;
  onDeleted: (id: string) => void;
}) {
  const [creating, setCreating] = useState(false);
  const [said, setSaid] = useState<string | null>(null);
  const newButton = useRef<HTMLButtonElement>(null);
  const list = useRef<HTMLUListElement>(null);
  useFocusOnReturn(newButton, creating);
  const focusAfterDelete = useFocusAfterDelete(list, newButton);

  const deleted = (group: Group, how: Removal) => {
    focusAfterDelete(
      groups.map((g) => g.id),
      group.id,
    );
    setSaid(
      how === 'gone'
        ? `The group “${group.name}” had already been deleted.`
        : `Deleted the group “${group.name}”.`,
    );
    onDeleted(group.id);
  };

  return (
    <Card title="Groups">
      <p className="organise-lead">
        Folders: a site is in at most one group. Use one per client, team or
        kind of site.
      </p>
      {groups.length === 0 ? (
        <p className="organise-empty">
          No groups yet. Every site is in No group until you make one.
        </p>
      ) : (
        <ul ref={list} className="organise-list" aria-label="Groups">
          {groups.map((group) => (
            <GroupRow
              key={group.id}
              api={api}
              group={group}
              count={counts[group.id] ?? 0}
              recount={recount}
              onRenamed={onRenamed}
              onDeleted={deleted}
            />
          ))}
        </ul>
      )}
      {creating ? (
        <NameForm
          label="New group name"
          hint={nameHint}
          submitLabel="Create group"
          busyLabel="Creating…"
          onCancel={() => setCreating(false)}
          onSubmit={async (name) => {
            const result = await attempt(() =>
              api.POST('/api/v0/groups', { body: { name } }),
            );
            if ('error' in result) return result.error;
            if (result.data) onCreated(result.data);
            setCreating(false);
            return null;
          }}
        />
      ) : (
        <div>
          <Button
            ref={newButton}
            type="button"
            variant="primary"
            onClick={() => setCreating(true)}
          >
            New group
          </Button>
        </div>
      )}
      <p
        className={said ? 'organise-said' : 'wl-visually-hidden'}
        role="status"
      >
        {said}
      </p>
    </Card>
  );
}

function GroupRow({
  api,
  group,
  count,
  recount,
  onRenamed,
  onDeleted,
}: {
  api: ApiClient;
  group: Group;
  count: number;
  recount: () => Promise<void>;
  onRenamed: (group: Group) => void;
  onDeleted: (group: Group, how: Removal) => void;
}) {
  const [renaming, setRenaming] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const renameButton = useRef<HTMLButtonElement>(null);
  useFocusOnReturn(renameButton, renaming);
  const path = { groupId: group.id };

  if (renaming)
    return (
      <li className="organise-row organise-row-editing">
        <NameForm
          label={`Rename ${group.name}`}
          hint={nameHint}
          initial={group.name}
          submitLabel="Save"
          busyLabel="Saving…"
          onCancel={() => setRenaming(false)}
          onSubmit={async (name) => {
            const result = await attempt(() =>
              api.PATCH('/api/v0/groups/{groupId}', {
                params: { path },
                body: { name },
              }),
            );
            if ('error' in result) return result.error;
            if (result.data) onRenamed(result.data);
            setRenaming(false);
            return null;
          }}
        />
      </li>
    );

  return (
    <li className="organise-row" data-id={group.id}>
      <span className="organise-hub" aria-hidden="true" />
      <span className="organise-name">
        <span className="organise-title">{group.name}</span>
        {group.note ? (
          <span className="organise-note">{group.note}</span>
        ) : null}
      </span>
      <SitesLink
        href={sitesHref({ g: group.id })}
        count={count}
        context={`in ${group.name}`}
      />
      <span className="organise-actions">
        <Button
          ref={renameButton}
          type="button"
          aria-label={`Rename ${group.name}`}
          onClick={() => setRenaming(true)}
        >
          Rename
        </Button>
        <Button
          type="button"
          aria-label={`Delete ${group.name}`}
          onClick={() => setDeleting(true)}
        >
          Delete
        </Button>
      </span>
      {deleting ? (
        <DeleteDialog
          title={`Delete the group “${group.name}”?`}
          confirmLabel="Delete group"
          recount={recount}
          onClose={() => setDeleting(false)}
          onConfirm={async () => {
            const result = await remove(() =>
              api.DELETE('/api/v0/groups/{groupId}', { params: { path } }),
            );
            if (typeof result === 'object') return result.error;
            onDeleted(group, result);
            return null;
          }}
        >
          <p>
            {count === 0
              ? 'It has no sites.'
              : `Its ${sites(count)} ${count === 1 ? 'moves' : 'move'} to No group.`}{' '}
            Sites are never deleted with a group.
          </p>
        </DeleteDialog>
      ) : null}
    </li>
  );
}
