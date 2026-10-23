// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import {
  Checkbox,
  cx,
  Menu,
  MenuItem,
  Popover,
  Sparkline,
  StatusTag,
  TagChip,
  Ward,
  Wire,
} from '@wardlock/ui';
import { memo, useEffect, useRef, useState } from 'react';
import { tagSlot } from '../shell/navigator.js';
import { navigate } from '../shell/router.js';
import { Time } from './format.js';
import { Star } from './site-page-header.js';
import type { Group, Tag } from './site-page-organise.js';
import { GroupPicker, TagsPicker } from './sites-page-pickers.js';
import {
  access,
  weekRequests,
  type SiteSummary,
} from './sites-page-sections.js';

/**
 * What a row, a section's Select all and the bulk bar can do. The page
 * keeps one stable object, so rows re-render only when their own site or
 * selection changes.
 */
export interface ListActions {
  select: (names: string[], on: boolean) => void;
  pin: (names: string[], pinned: boolean) => void;
  tag: (names: string[], tag: Tag, add: boolean) => void;
  createTag: (names: string[], name: string) => void;
  move: (names: string[], group: Group | undefined) => void;
  createGroup: (names: string[], name: string) => void;
  copy: (site: SiteSummary) => void;
}

const methodWords = {
  links: 'share links',
  password: 'password',
  email: 'email code',
  oauth: 'sign-in',
} as const;

const methodOrder = ['links', 'password', 'email', 'oauth'] as const;

const compactNumber = new Intl.NumberFormat('en-NZ', {
  notation: 'compact',
  maximumFractionDigits: 1,
});
const fullNumber = new Intl.NumberFormat('en-NZ');

/** How a row shows at most three tags: the rest as "+N". */
const shownTags = 3;

function AccessCell({ site }: { site: SiteSummary }) {
  const word =
    site.visibility === 'public' ? (
      <StatusTag tone="public">Public</StatusTag>
    ) : (
      <StatusTag tone="ward">
        <Ward size="sm" />
        Protected
      </StatusTag>
    );
  const methods =
    site.visibility === 'protected'
      ? methodOrder
          .filter((m) => site.methods.includes(m))
          .map((m) => methodWords[m])
          .join(' · ')
      : 'anyone with the address';
  return (
    <div className="sites-row-access">
      <span className="sites-row-words">
        {word}
        {site.unavailable ? (
          <StatusTag tone="danger">Taken down</StatusTag>
        ) : null}
      </span>
      <span className="sites-row-sub">{methods}</span>
    </div>
  );
}

function LinksCell({ site }: { site: SiteSummary }) {
  const count = site.workingLinks;
  return (
    <div className="sites-row-links">
      <span>
        {count === 0
          ? 'No links'
          : `${count} ${count === 1 ? 'link' : 'links'}`}
      </span>
      {site.expiringLinks > 0 ? (
        <span
          title={`${site.expiringLinks} expiring within 48 hours`}
          className="sites-row-soon"
        >
          <StatusTag tone="warning">Soon</StatusTag>
          <span className="wl-visually-hidden">
            , {site.expiringLinks} expiring within 48 hours
          </span>
        </span>
      ) : null}
    </div>
  );
}

function RequestsCell({ site }: { site: SiteSummary }) {
  const total = weekRequests(site);
  return (
    <div
      className="sites-row-requests"
      title={`${fullNumber.format(total)} requests in 7 days`}
    >
      <Sparkline values={site.requests} />
      <span>
        {compactNumber.format(total)}
        <span className="wl-visually-hidden"> requests in 7 days</span>
      </span>
    </div>
  );
}

function DeployCell({ site }: { site: SiteSummary }) {
  const live = site.liveDeploy;
  if (!live)
    return (
      <div className="sites-row-deploy">
        <span className="sites-row-sub sites-row-none">Not deployed yet</span>
      </div>
    );
  return (
    <div className="sites-row-deploy">
      <span className="sites-row-message" title={live.message}>
        {live.message ?? 'No message'}
      </span>
      <span className="sites-row-sub">
        <span className="sites-mono">{live.shortId}</span> ·{' '}
        <Time at={live.activatedAt} />
      </span>
    </div>
  );
}

type MenuMode = 'menu' | 'tags' | 'group';

/** The row's ⋯ menu; Edit tags and Move to group open their pickers in place. */
function RowMenu({
  site,
  tags,
  groups,
  actions,
}: {
  site: SiteSummary;
  tags: Tag[];
  groups: Group[];
  actions: ListActions;
}) {
  const [open, setOpen] = useState(false);
  const [mode, setMode] = useState<MenuMode>('menu');
  const body = useRef<HTMLDivElement>(null);
  const names = [site.name];
  // A picker opened from the menu takes focus as the menu did.
  useEffect(() => {
    if (open && mode !== 'menu')
      body.current
        ?.querySelector<HTMLElement>('input, [role="menuitemcheckbox"]')
        ?.focus();
  }, [open, mode]);
  // After an action the menu closes with focus back on its ⋯ button.
  const close = () => {
    body.current
      ?.closest('[data-site]')
      ?.querySelector<HTMLElement>('.sites-row-more')
      ?.focus();
    setOpen(false);
    setMode('menu');
  };
  const label =
    mode === 'tags'
      ? `Tags for ${site.name}`
      : mode === 'group'
        ? `Move ${site.name} to a group`
        : `Actions for ${site.name}`;
  return (
    <Popover
      label={label}
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (!next) setMode('menu');
      }}
      trigger={(props) => (
        <button
          {...props}
          type="button"
          className="sites-row-more"
          aria-label={`Actions for ${site.name}`}
        >
          <span aria-hidden="true">⋯</span>
        </button>
      )}
    >
      <div ref={body}>
        {mode === 'menu' ? (
          <Menu label={`Actions for ${site.name}`}>
            <MenuItem
              onSelect={() =>
                navigate(`/sites/${encodeURIComponent(site.name)}`)
              }
            >
              Open
            </MenuItem>
            <MenuItem
              onSelect={() => {
                close();
                actions.pin(names, !site.pinned);
              }}
            >
              {site.pinned ? 'Unpin' : 'Pin'}
            </MenuItem>
            <MenuItem onSelect={() => setMode('tags')}>Edit tags…</MenuItem>
            <MenuItem onSelect={() => setMode('group')}>
              Move to group…
            </MenuItem>
            <MenuItem
              onSelect={() => {
                close();
                actions.copy(site);
              }}
            >
              Copy address
            </MenuItem>
            <MenuItem
              onSelect={() =>
                navigate(`/sites/${encodeURIComponent(site.name)}/access-log`)
              }
            >
              Access log
            </MenuItem>
          </Menu>
        ) : mode === 'tags' ? (
          <TagsPicker
            targets={[site]}
            tags={tags}
            onToggle={(tag, add) => actions.tag(names, tag, add)}
            onCreate={(name) => actions.createTag(names, name)}
          />
        ) : (
          <GroupPicker
            targets={[site]}
            groups={groups}
            onMove={(group) => {
              close();
              actions.move(names, group);
            }}
            onCreate={(name) => {
              close();
              actions.createGroup(names, name);
            }}
          />
        )}
      </div>
    </Popover>
  );
}

/**
 * One site on its wire: select box, name with its pin, address, up to three
 * tags, who can open it, working links, 7-day requests, the live deploy and
 * its ⋯ menu.
 */
export const SiteRow = memo(function SiteRow({
  site,
  selected,
  tagsById,
  tags,
  groups,
  actions,
}: {
  site: SiteSummary;
  selected: boolean;
  tagsById: Map<string, Tag>;
  tags: Tag[];
  groups: Group[];
  actions: ListActions;
}) {
  const kind = access(site);
  const siteTags = site.tagIds.flatMap((id) => {
    const tag = tagsById.get(id);
    return tag ? [tag] : [];
  });
  return (
    <Wire
      tone={kind === 'public' ? 'public' : 'default'}
      ward={kind === 'protected'}
    >
      <div
        className={cx('sites-row', selected ? 'sites-row-selected' : null)}
        data-site={site.name}
      >
        <Checkbox
          hideLabel
          label={`Select ${site.name}`}
          className="sites-row-check"
          checked={selected}
          onChange={(event) =>
            actions.select([site.name], event.target.checked)
          }
        />
        <div className="sites-row-site">
          <span className="sites-row-title">
            <a
              className="sites-row-name"
              title={site.name}
              href={`/sites/${encodeURIComponent(site.name)}`}
            >
              {site.name}
            </a>
            <button
              type="button"
              className="sites-pin"
              aria-pressed={site.pinned}
              aria-label={`Pin ${site.name}`}
              title={site.pinned ? 'Pinned' : 'Not pinned'}
              onClick={() => actions.pin([site.name], !site.pinned)}
            >
              <Star filled={site.pinned} />
            </button>
          </span>
          <span
            className="sites-row-sub sites-mono sites-row-host"
            title={site.hostname}
          >
            {site.hostname}
          </span>
        </div>
        <div className="sites-row-tags">
          {siteTags.slice(0, shownTags).map((tag) => (
            <TagChip key={tag.id} label={tag.name} slot={tagSlot(tag.colour)} />
          ))}
          {siteTags.length > shownTags ? (
            <span
              className="sites-row-extra"
              title={siteTags
                .slice(shownTags)
                .map((tag) => tag.name)
                .join(', ')}
            >
              +{siteTags.length - shownTags}
              <span className="wl-visually-hidden"> more tags</span>
            </span>
          ) : null}
        </div>
        <AccessCell site={site} />
        <LinksCell site={site} />
        <RequestsCell site={site} />
        <DeployCell site={site} />
        <RowMenu site={site} tags={tags} groups={groups} actions={actions} />
      </div>
    </Wire>
  );
});
