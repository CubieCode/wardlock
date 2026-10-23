// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient, components } from '@wardlock/apiclient-ts';
import {
  Button,
  Card,
  Fieldset,
  Popover,
  Radio,
  TagChip,
  type TagSlot,
} from '@wardlock/ui';
import { useRef, useState } from 'react';
import { sitesHref, tagSlot } from '../shell/navigator.js';
import { useFocusAfterDelete, useFocusOnReturn } from './focus.js';
import {
  attempt,
  DeleteDialog,
  NameForm,
  remove,
  SitesLink,
  type Removal,
} from './shared.js';

export type Tag = components['schemas']['Tag'];

const nameHint = 'Lowercase letters, digits and hyphens, up to 30 characters.';

/** The colour slots in picker order: the eight colours, then neutral. */
const slots: TagSlot[] = [1, 2, 3, 4, 5, 6, 7, 8, 0];

/** A slot in words, for the picker and screen readers. */
export const slotName = (slot: TagSlot) =>
  slot === 0 ? 'Neutral' : `Colour ${slot}`;

/** The owner's tags, with New tag, Colour, Rename and Delete. */
export function TagsCard({
  api,
  tags,
  counts,
  recount,
  onCreated,
  onChanged,
  onDeleted,
}: {
  api: ApiClient;
  tags: Tag[];
  /** Sites with each tag, by tag ID. */
  counts: Record<string, number>;
  /** Reads the site counts again. */
  recount: () => Promise<void>;
  onCreated: (tag: Tag) => void;
  onChanged: (tag: Tag) => void;
  onDeleted: (id: string) => void;
}) {
  const [creating, setCreating] = useState(false);
  const [said, setSaid] = useState<string | null>(null);
  const newButton = useRef<HTMLButtonElement>(null);
  const list = useRef<HTMLUListElement>(null);
  useFocusOnReturn(newButton, creating);
  const focusAfterDelete = useFocusAfterDelete(list, newButton);

  const deleted = (tag: Tag, how: Removal) => {
    focusAfterDelete(
      tags.map((t) => t.id),
      tag.id,
    );
    setSaid(
      how === 'gone'
        ? `The tag “${tag.name}” had already been deleted.`
        : `Deleted the tag “${tag.name}”.`,
    );
    onDeleted(tag.id);
  };

  return (
    <Card title="Tags">
      <p className="organise-lead">
        Labels: a site can have many. A new tag takes the next of eight colours;
        after that, tags share neutral.
      </p>
      {tags.length === 0 ? (
        <p className="organise-empty">No tags yet.</p>
      ) : (
        <ul ref={list} className="organise-list" aria-label="Tags">
          {tags.map((tag) => (
            <TagRow
              key={tag.id}
              api={api}
              tag={tag}
              count={counts[tag.id] ?? 0}
              recount={recount}
              onChanged={onChanged}
              onDeleted={deleted}
            />
          ))}
        </ul>
      )}
      {creating ? (
        <NameForm
          label="New tag name"
          hint={nameHint}
          submitLabel="Create tag"
          busyLabel="Creating…"
          onCancel={() => setCreating(false)}
          onSubmit={async (name) => {
            const result = await attempt(() =>
              api.POST('/api/v0/tags', { body: { name } }),
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
            New tag
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

function TagRow({
  api,
  tag,
  count,
  recount,
  onChanged,
  onDeleted,
}: {
  api: ApiClient;
  tag: Tag;
  count: number;
  recount: () => Promise<void>;
  onChanged: (tag: Tag) => void;
  onDeleted: (tag: Tag, how: Removal) => void;
}) {
  const [renaming, setRenaming] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const renameButton = useRef<HTMLButtonElement>(null);
  useFocusOnReturn(renameButton, renaming);
  const path = { tagId: tag.id };
  const slot = tagSlot(tag.colour);

  if (renaming)
    return (
      <li className="organise-row organise-row-editing">
        <NameForm
          label={`Rename ${tag.name}`}
          hint={nameHint}
          initial={tag.name}
          submitLabel="Save"
          busyLabel="Saving…"
          onCancel={() => setRenaming(false)}
          onSubmit={async (name) => {
            const result = await attempt(() =>
              api.PATCH('/api/v0/tags/{tagId}', {
                params: { path },
                body: { name },
              }),
            );
            if ('error' in result) return result.error;
            if (result.data) onChanged(result.data);
            setRenaming(false);
            return null;
          }}
        />
      </li>
    );

  return (
    <li className="organise-row" data-id={tag.id}>
      <span className="organise-name">
        <TagChip label={tag.name} slot={slot} />
        <span className="wl-visually-hidden">, {slotName(slot)}</span>
      </span>
      <SitesLink
        href={sitesHref({ t: tag.id })}
        count={count}
        context={`tagged ${tag.name}`}
      />
      <span className="organise-actions">
        <ColourPicker
          tag={tag}
          onPick={async (colour) => {
            setError(null);
            const result = await attempt(() =>
              api.PATCH('/api/v0/tags/{tagId}', {
                params: { path },
                body: { colour },
              }),
            );
            if ('error' in result) {
              setError(`Could not change the colour: ${result.error}`);
              return false;
            }
            if (result.data) onChanged(result.data);
            return true;
          }}
        />
        <Button
          ref={renameButton}
          type="button"
          aria-label={`Rename ${tag.name}`}
          onClick={() => setRenaming(true)}
        >
          Rename
        </Button>
        <Button
          type="button"
          aria-label={`Delete ${tag.name}`}
          onClick={() => setDeleting(true)}
        >
          Delete
        </Button>
      </span>
      {error ? (
        <p className="organise-error" role="alert">
          {error}
        </p>
      ) : null}
      {deleting ? (
        <DeleteDialog
          title={`Delete the tag “${tag.name}”?`}
          confirmLabel="Delete tag"
          recount={recount}
          onClose={() => setDeleting(false)}
          onConfirm={async () => {
            const result = await remove(() =>
              api.DELETE('/api/v0/tags/{tagId}', { params: { path } }),
            );
            if (typeof result === 'object') return result.error;
            onDeleted(tag, result);
            return null;
          }}
        >
          <p>
            {count === 0
              ? 'No sites have it.'
              : count === 1
                ? 'Its site loses the tag.'
                : `Its ${count} sites lose the tag.`}{' '}
            Sites are never deleted with a tag.
          </p>
        </DeleteDialog>
      ) : null}
    </li>
  );
}

/**
 * "Colour": the eight colours and neutral as one radio group, each choice
 * showing the tag itself in that colour, so a colour never appears without
 * the name. A choice saves at once; while one is saving, the latest choice
 * waits and is saved next, so none is dropped.
 */
function ColourPicker({
  tag,
  onPick,
}: {
  tag: Tag;
  /** Saves a colour, answering whether it was saved. */
  onPick: (slot: TagSlot) => Promise<boolean>;
}) {
  const [open, setOpen] = useState(false);
  // The choice being saved or waiting to be; null shows the tag as it is.
  const [chosen, setChosen] = useState<TagSlot | null>(null);
  const saving = useRef(false);
  const next = useRef<TagSlot | null>(null);
  const current = tagSlot(tag.colour);
  const shown = chosen ?? current;

  const pick = async (slot: TagSlot) => {
    setChosen(slot);
    next.current = slot;
    if (saving.current) return;
    saving.current = true;
    let saved = current;
    while (next.current !== null) {
      const slot = next.current;
      next.current = null;
      if (slot === saved) continue;
      if (await onPick(slot)) saved = slot;
    }
    saving.current = false;
    setChosen(null);
  };

  return (
    <Popover
      label={`Colour for ${tag.name}`}
      open={open}
      onOpenChange={setOpen}
      trigger={(props) => (
        <Button {...props} type="button" aria-label={`Colour of ${tag.name}`}>
          Colour
        </Button>
      )}
    >
      <div className="organise-colours">
        <Fieldset legend={`Colour for ${tag.name}`}>
          {slots.map((slot) => (
            <Radio
              key={slot}
              name={`colour-${tag.id}`}
              label={slotName(slot)}
              hint={<TagChip label={tag.name} slot={slot} />}
              checked={slot === shown}
              onChange={() => void pick(slot)}
            />
          ))}
        </Fieldset>
      </div>
    </Popover>
  );
}
