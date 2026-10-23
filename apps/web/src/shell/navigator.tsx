// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient, components } from '@wardlock/apiclient-ts';
import { NavItem, NavList, type TagSlot } from '@wardlock/ui';
import { useEffect, useState, type ReactNode } from 'react';
import {
  clockIcon,
  downIcon,
  globeIcon,
  listIcon,
  pinIcon,
  quietIcon,
  searchIcon,
  untaggedIcon,
} from './icons.js';
import { useLocation } from './router.js';
import { shellRoutes } from './routes.js';

type SiteCounts = components['schemas']['SiteCounts'];
type SiteView = components['schemas']['SiteView'];
type Group = components['schemas']['Group'];
type Tag = components['schemas']['Tag'];

/**
 * Whether the navigator offers saved views, groups and tags: on since the
 * sites list filters by `g`, `t` and `v` (V5.8).
 */
export const navigatorFilters = true;

/** The saved views in navigator order (docs/SPEC.md, "Saved views"). */
export const savedViews: { view: SiteView; label: string; icon: ReactNode }[] =
  [
    { view: 'pinned', label: 'Pinned', icon: pinIcon },
    { view: 'expiring', label: 'Links expiring soon', icon: clockIcon },
    { view: 'public', label: 'Public', icon: globeIcon },
    { view: 'quiet', label: 'No requests in 30 days', icon: quietIcon },
    { view: 'untagged', label: 'Untagged', icon: untaggedIcon },
    { view: 'down', label: 'Taken down', icon: downIcon },
  ];

/**
 * A sites list address: filtered by a group (`none`), tag, view or search,
 * and drawn grouped (`by`), sorted and at a density (`d`). Empty values are
 * left out.
 */
export function sitesHref(
  filter?: Partial<
    Record<'g' | 't' | 'v' | 'q' | 'by' | 'sort' | 'd', string | undefined>
  >,
) {
  const query = new URLSearchParams();
  for (const [key, value] of Object.entries(filter ?? {}))
    if (value) query.set(key, value);
  const text = query.toString();
  return text ? `/sites?${text}` : '/sites';
}

const sitesChangedEvent = 'wardlock:sites-changed';

/**
 * Tells the navigator that sites were filed, tagged or pinned, so it counts
 * them again.
 */
export function sitesChanged() {
  window.dispatchEvent(new Event(sitesChangedEvent));
}

/** The organise screen, once it is built (V5.7). */
const organisePath = '/organise';
const organiseBuilt = () =>
  shellRoutes.some((route) => route.path === organisePath);

interface Data {
  counts: SiteCounts;
  groups: Group[];
  tags: Tag[];
}

export type NavigatorState =
  { kind: 'loading' } | { kind: 'error' } | ({ kind: 'ready' } & Data);

async function load(api: ApiClient, filters: boolean): Promise<Data> {
  const [counts, groups, tags] = await Promise.all([
    api.GET('/api/v0/site-counts'),
    filters ? api.GET('/api/v0/groups') : undefined,
    filters ? api.GET('/api/v0/tags') : undefined,
  ]);
  if (!counts.data || (filters && (!groups?.data || !tags?.data)))
    throw new Error('could not count sites');
  return {
    counts: counts.data,
    groups: groups?.data?.items ?? [],
    tags: tags?.data?.items ?? [],
  };
}

/**
 * Loads what the navigator shows: the counts and, with `filters`, the group
 * and tag names. Called once by the shell, so the navigator column and its
 * drawer share one read. Nothing loads while `enabled` is false.
 */
export function useNavigatorData(
  api: ApiClient,
  enabled: boolean,
  filters = navigatorFilters,
) {
  const [state, setState] = useState<NavigatorState>({ kind: 'loading' });
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    if (!enabled) return;
    let live = true;
    load(api, filters).then(
      (data) => live && setState({ kind: 'ready', ...data }),
      () => live && setState({ kind: 'error' }),
    );
    return () => {
      live = false;
    };
  }, [api, enabled, filters, attempt]);
  // A change on the sites list counts again, keeping the old counts shown
  // until the new ones arrive.
  useEffect(() => {
    if (!enabled) return;
    const recount = () => setAttempt((n) => n + 1);
    window.addEventListener(sitesChangedEvent, recount);
    return () => window.removeEventListener(sitesChangedEvent, recount);
  }, [enabled]);
  const retry = () => {
    setState({ kind: 'loading' });
    setAttempt((n) => n + 1);
  };
  return { state, retry };
}

/**
 * The sites navigator beside the sites pages: Jump to, All sites and, once
 * the list can show them, the saved views, groups and tags with how many
 * sites each holds. A view with no sites is left out, except Pinned.
 */
export function SitesNavigator({
  state,
  onRetry,
  onJump,
  filters = navigatorFilters,
}: {
  state: NavigatorState;
  onRetry: () => void;
  onJump: () => void;
  filters?: boolean;
}) {
  const { pathname, params } = useLocation();
  const onList = pathname === '/sites';
  const g = params.get('g');
  const t = params.get('t');
  const v = params.get('v');
  const current = (filter: { g?: string; t?: string; v?: string }) =>
    onList &&
    g === (filter.g ?? null) &&
    t === (filter.t ?? null) &&
    v === (filter.v ?? null);
  const ready = state.kind === 'ready' ? state : undefined;
  const organise = organiseBuilt();

  return (
    <>
      <button type="button" className="shell-jump" onClick={onJump}>
        <span className="shell-nav-icon" aria-hidden="true">
          {searchIcon}
        </span>
        Jump to…
        <kbd className="wl-kbd" aria-hidden="true">
          {shortcutLabel()}
        </kbd>
      </button>
      <NavList label="Sites">
        <Row
          href={sitesHref()}
          current={current({})}
          icon={listIcon}
          name="All sites"
          count={ready?.counts.all}
        />
        {filters && ready
          ? savedViews.map(({ view, label, icon }) =>
              ready.counts.views[view] > 0 || view === 'pinned' ? (
                <Row
                  key={view}
                  href={sitesHref({ v: view })}
                  current={current({ v: view })}
                  icon={icon}
                  name={label}
                  count={ready.counts.views[view]}
                />
              ) : null,
            )
          : null}
      </NavList>
      {state.kind === 'error' ? (
        <p className="shell-nav-error" role="status">
          Could not count your sites.{' '}
          <button type="button" className="shell-nav-retry" onClick={onRetry}>
            Count again
          </button>
        </p>
      ) : null}
      {filters && ready && (ready.groups.length > 0 || organise) ? (
        <Section
          title="Groups"
          action={
            organise ? (
              <a className="shell-nav-action" href={organisePath}>
                New group
              </a>
            ) : null
          }
        >
          <NavList label="Groups">
            {ready.groups.map((group) => (
              <Row
                key={group.id}
                href={sitesHref({ g: group.id })}
                current={current({ g: group.id })}
                icon={<span className="shell-nav-hub" />}
                name={group.name}
                count={countOf(ready.counts.groups, 'groupId', group.id)}
              />
            ))}
            {ready.groups.length > 0 && ready.counts.noGroup > 0 ? (
              <Row
                href={sitesHref({ g: 'none' })}
                current={current({ g: 'none' })}
                icon={<span className="shell-nav-hub shell-nav-hub-none" />}
                name="No group"
                count={ready.counts.noGroup}
              />
            ) : null}
          </NavList>
        </Section>
      ) : null}
      {filters && ready && ready.tags.length > 0 ? (
        <Section
          title="Tags"
          action={
            organise ? (
              <a className="shell-nav-action" href={organisePath}>
                Manage tags
              </a>
            ) : null
          }
        >
          <NavList label="Tags">
            {ready.tags.map((tag) => (
              <Row
                key={tag.id}
                href={sitesHref({ t: tag.id })}
                current={current({ t: tag.id })}
                icon={
                  <span
                    className="wl-tag-dot"
                    style={{
                      background: `var(--color-tag-${tagSlot(tag.colour)})`,
                    }}
                  />
                }
                name={tag.name}
                count={countOf(ready.counts.tags, 'tagId', tag.id)}
              />
            ))}
          </NavList>
        </Section>
      ) : null}
    </>
  );
}

function Row({
  href,
  current,
  icon,
  name,
  count,
}: {
  href: string;
  current: boolean;
  icon: ReactNode;
  name: string;
  /** Undefined while the counts load. */
  count: number | undefined;
}) {
  return (
    <NavItem href={href} current={current} className="shell-nav-row">
      <span className="shell-nav-icon" aria-hidden="true">
        {icon}
      </span>
      <span className="shell-nav-name">{name}</span>
      {count === undefined ? null : (
        <>
          {' '}
          <span className="shell-nav-count">{count}</span>
        </>
      )}
    </NavItem>
  );
}

function Section({
  title,
  action,
  children,
}: {
  title: string;
  action: ReactNode;
  children: ReactNode;
}) {
  return (
    <section className="shell-nav-section">
      <div className="shell-nav-heading">
        <h2>{title}</h2>
        {action}
      </div>
      {children}
    </section>
  );
}

function countOf<K extends 'groupId' | 'tagId'>(
  rows: ({ sites: number } & Record<K, string>)[],
  key: K,
  id: string,
) {
  return rows.find((row) => row[key] === id)?.sites ?? 0;
}

/** A tag's colour slot, neutral for anything outside the palette. */
export function tagSlot(colour: number): TagSlot {
  return Number.isInteger(colour) && colour >= 0 && colour <= 8
    ? (colour as TagSlot)
    : 0;
}

/** Apple platforms take ⌘K for jump-to; everything else Ctrl+K. */
function applePlatform() {
  return /Mac|iPhone|iPad/.test(navigator.userAgent);
}

/** The jump-to shortcut as this platform writes it. */
export function shortcutLabel() {
  return applePlatform() ? '⌘K' : 'Ctrl K';
}

/** The jump-to shortcut in `aria-keyshortcuts` form. */
export function shortcutKeys() {
  return applePlatform() ? 'Meta+K' : 'Control+K';
}

/** Whether a key press is this platform's jump-to shortcut, and only that. */
export function isJumpShortcut(event: KeyboardEvent) {
  if (event.repeat || event.isComposing || event.key.toLowerCase() !== 'k')
    return false;
  if (event.altKey || event.shiftKey) return false;
  return applePlatform()
    ? event.metaKey && !event.ctrlKey
    : event.ctrlKey && !event.metaKey;
}
