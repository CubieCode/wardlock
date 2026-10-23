// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import { Menu, MenuCheckboxItem, MenuItem, PopoverFilter } from '@wardlock/ui';
import { useState } from 'react';
import { tagSlot } from '../shell/navigator.js';
import type { Group, Tag } from './site-page-organise.js';
import { siteCount, triState } from './sites-page-organise.js';
import type { SiteSummary } from './sites-page-sections.js';

/** Tag names: 1 to 30 lowercase letters, digits and hyphens (SPEC). */
const tagName = /^[a-z0-9-]{1,30}$/;
/** Group names: 1 to 40 characters with no control characters (SPEC). */
const groupName = (name: string) =>
  name.length >= 1 &&
  [...name].length <= 40 &&
  ![...name].some((c) => c < ' ' || c === '\x7f');
const fold = (name: string) => name.normalize('NFC').toLowerCase();

/** "2 of 5" beside a choice some of the sites have. */
function mixedHint(state: boolean | 'mixed', have: number, of: number) {
  return state === 'mixed' ? `${have} of ${of}` : undefined;
}

/**
 * The owner's tags, each checked when every target has it and mixed when
 * some do. Choosing a tag every target has takes it off them all; any other
 * choice gives it to them all. A valid new name in the filter offers Create
 * tag.
 */
export function TagsPicker({
  targets,
  tags,
  onToggle,
  onCreate,
}: {
  targets: SiteSummary[];
  tags: Tag[];
  onToggle: (tag: Tag, add: boolean) => void;
  onCreate: (name: string) => void;
}) {
  const [filter, setFilter] = useState('');
  const query = filter.trim().toLowerCase();
  const shown = tags.filter((tag) => tag.name.includes(query));
  const canCreate =
    query !== '' && tagName.test(query) && !tags.some((t) => t.name === query);
  return (
    <>
      <p className="sites-pick-title">
        Tags for {siteCount(targets.map((s) => s.name))}
      </p>
      <PopoverFilter
        label="Find or create a tag"
        placeholder="Tag name"
        value={filter}
        onChange={setFilter}
      />
      {shown.length > 0 || canCreate ? (
        <Menu label="Tags">
          {shown.map((tag) => {
            const state = triState(targets, (s) => s.tagIds.includes(tag.id));
            const have = targets.filter((s) =>
              s.tagIds.includes(tag.id),
            ).length;
            return (
              <MenuCheckboxItem
                key={tag.id}
                checked={state}
                hint={mixedHint(state, have, targets.length)}
                onSelect={() => onToggle(tag, state !== true)}
              >
                <span
                  className="wl-tag-dot"
                  style={{
                    background: `var(--color-tag-${tagSlot(tag.colour)})`,
                  }}
                  aria-hidden="true"
                />{' '}
                {tag.name}
              </MenuCheckboxItem>
            );
          })}
          {canCreate ? (
            <MenuItem
              icon="+"
              onSelect={() => {
                onCreate(query);
                setFilter('');
              }}
            >
              Create tag “{query}”
            </MenuItem>
          ) : null}
        </Menu>
      ) : (
        <p className="sites-pick-hint">
          {query
            ? 'Tag names use lowercase letters, digits and hyphens, up to 30 characters.'
            : 'No tags yet. Type a name to create one.'}
        </p>
      )}
      {targets.length > 1 ? (
        <p className="sites-pick-hint">
          A dash means some of the sites have it. Choose it to give it to all of
          them.
        </p>
      ) : null}
    </>
  );
}

/**
 * No group and the owner's groups, each checked when every target is in it
 * and mixed when some are. Choosing one moves every target there. A new
 * name in the filter offers Create group.
 */
export function GroupPicker({
  targets,
  groups,
  onMove,
  onCreate,
}: {
  targets: SiteSummary[];
  groups: Group[];
  onMove: (group: Group | undefined) => void;
  onCreate: (name: string) => void;
}) {
  const [filter, setFilter] = useState('');
  const query = filter.trim();
  const folded = fold(query);
  const options: (Group | undefined)[] = [undefined, ...groups];
  const shown = options.filter((group) =>
    fold(group ? group.name : 'No group').includes(folded),
  );
  const canCreate =
    query !== '' &&
    groupName(query) &&
    !groups.some((g) => fold(g.name) === folded);
  return (
    <>
      <p className="sites-pick-title">
        Move {siteCount(targets.map((s) => s.name))} to
      </p>
      <PopoverFilter
        label="Find or create a group"
        placeholder="Group name"
        value={filter}
        onChange={setFilter}
      />
      {shown.length > 0 || canCreate ? (
        <Menu label="Groups">
          {shown.map((group) => {
            const id = group?.id;
            const state = triState(targets, (s) => s.groupId === id);
            const have = targets.filter((s) => s.groupId === id).length;
            return (
              <MenuCheckboxItem
                key={id ?? 'none'}
                checked={state}
                hint={mixedHint(state, have, targets.length)}
                onSelect={() => onMove(group)}
              >
                {group ? group.name : 'No group'}
              </MenuCheckboxItem>
            );
          })}
          {canCreate ? (
            <MenuItem
              icon="+"
              onSelect={() => {
                onCreate(query);
                setFilter('');
              }}
            >
              Create group “{query}”
            </MenuItem>
          ) : null}
        </Menu>
      ) : (
        <p className="sites-pick-hint">Group names are up to 40 characters.</p>
      )}
    </>
  );
}

/**
 * Pinned, checked when every target is pinned and mixed when some are.
 * Choosing it unpins them all when all are pinned, and otherwise pins them
 * all.
 */
export function PinPicker({
  targets,
  onPin,
}: {
  targets: SiteSummary[];
  onPin: (pinned: boolean) => void;
}) {
  const state = triState(targets, (s) => s.pinned);
  const have = targets.filter((s) => s.pinned).length;
  return (
    <Menu label="Pin">
      <MenuCheckboxItem
        checked={state}
        hint={mixedHint(state, have, targets.length)}
        onSelect={() => onPin(state !== true)}
      >
        Pinned
      </MenuCheckboxItem>
    </Menu>
  );
}
