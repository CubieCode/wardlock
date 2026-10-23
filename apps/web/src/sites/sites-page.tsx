// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient } from '@wardlock/apiclient-ts';
import {
  BulkBar,
  Button,
  Checkbox,
  cx,
  EmptyState,
  HubLayout,
  Notice,
  PageHeader,
  Popover,
  SegmentedControl,
  SelectField,
  Toast,
  Wire,
} from '@wardlock/ui';
import {
  memo,
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
  type RefObject,
} from 'react';
import { savedViews, sitesChanged } from '../shell/navigator.js';
import { useTitle } from '../shell/page.js';
import { navigate, useLocation } from '../shell/router.js';
import { useFlash } from './flash.js';
import type { Group, Tag } from './site-page-organise.js';
import { useMeta, useSites, type MetaState } from './sites-page-data.js';
import {
  applyChange,
  inverse,
  siteCount,
  type OrganiseRequest,
} from './sites-page-organise.js';
import { GroupPicker, PinPicker, TagsPicker } from './sites-page-pickers.js';
import {
  filtered,
  queryHref,
  readQuery,
  searchTokens,
  unfiltered,
  withoutWord,
  type Density,
  type GroupBy,
  type ListQuery,
  type SiteSort,
} from './sites-page-query.js';
import { SiteRow, type ListActions } from './sites-page-row.js';
import {
  buildSections,
  shownSites,
  weekRequests,
  type Section,
} from './sites-page-sections.js';
import './sites.css';
import './sites-page.css';

// Below 48rem a section shows three rows before Show all, from 48rem five.
const wideQuery = '(min-width: 48rem)';
function subscribeWide(onChange: () => void) {
  if (!window.matchMedia) return () => {};
  const media = window.matchMedia(wideQuery);
  media.addEventListener('change', onChange);
  return () => media.removeEventListener('change', onChange);
}
const wideSnapshot = () =>
  window.matchMedia ? window.matchMedia(wideQuery).matches : true;

/** How long typing pauses before the search is sent. */
const searchDelay = 250;

const groupings: { value: GroupBy; label: string }[] = [
  { value: 'group', label: 'Group' },
  { value: 'tag', label: 'Tag' },
  { value: 'access', label: 'Access' },
  { value: 'none', label: 'Nothing' },
];
const sorts: { value: SiteSort; label: string }[] = [
  { value: 'deploy', label: 'Last deploy' },
  { value: 'name', label: 'Name' },
  { value: 'requests', label: 'Requests' },
  { value: 'links', label: 'Links' },
];
const densities: { value: Density; label: string }[] = [
  { value: 'comfortable', label: 'Comfortable' },
  { value: 'compact', label: 'Compact' },
];

const plural = (n: number, one: string, many = `${one}s`) =>
  `${new Intl.NumberFormat('en-NZ').format(n)} ${n === 1 ? one : many}`;

/** The list's heading: what its filter from the navigator shows. */
function listTitle(query: ListQuery, meta: MetaState) {
  const ready = meta.kind === 'ready' ? meta : undefined;
  if (query.g === 'none') return 'No group';
  if (query.g)
    return ready?.groups.find((g) => g.id === query.g)?.name ?? 'Sites';
  if (query.t) {
    const tag = ready?.tags.find((t) => t.id === query.t);
    return tag ? `Tagged ${tag.name}` : 'Sites';
  }
  if (query.v)
    return savedViews.find((view) => view.view === query.v)?.label ?? 'Sites';
  return 'Sites';
}

/** The filters from the navigator and the search's tokens, as chips. */
function filterChips(query: ListQuery, meta: MetaState) {
  const ready = meta.kind === 'ready' ? meta : undefined;
  const chips: { key: string; label: string; without: ListQuery }[] = [];
  if (query.g)
    chips.push({
      key: 'g',
      label:
        query.g === 'none'
          ? 'No group'
          : `Group: ${ready?.groups.find((g) => g.id === query.g)?.name ?? 'unknown'}`,
      without: { ...query, g: undefined },
    });
  if (query.t)
    chips.push({
      key: 't',
      label: `Tag: ${ready?.tags.find((t) => t.id === query.t)?.name ?? 'unknown'}`,
      without: { ...query, t: undefined },
    });
  if (query.v)
    chips.push({
      key: 'v',
      label: savedViews.find((view) => view.view === query.v)?.label ?? query.v,
      without: { ...query, v: undefined },
    });
  for (const [i, token] of searchTokens(query.q).entries())
    chips.push({
      key: `q${i}`,
      label: token.label,
      without: { ...query, q: withoutWord(query.q, token.word) },
    });
  return chips;
}

const isTyping = (target: EventTarget | null) =>
  target instanceof HTMLElement &&
  (target.isContentEditable ||
    ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName));

/** `/` focuses the search, unless the owner is typing or a dialog is open. */
function useSlashFocus(input: RefObject<HTMLInputElement | null>) {
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (
        event.key !== '/' ||
        event.metaKey ||
        event.ctrlKey ||
        event.altKey ||
        isTyping(event.target) ||
        document.querySelector('dialog[open]')
      )
        return;
      event.preventDefault();
      input.current?.focus();
      input.current?.select();
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [input]);
}

/** The set with `key` added, or taken out if it was there. */
function toggled(set: Set<string>, key: string) {
  const next = new Set(set);
  if (next.has(key)) next.delete(key);
  else next.add(key);
  return next;
}

interface ToastState {
  message: string;
  key: number;
  tone: 'info' | 'danger';
  undo?: OrganiseRequest[] | undefined;
}

/** The most sites one change can name (`organiseSites`). */
export const maxSelection = 500;

/** After a change to one row, where focus goes if the row moved or went. */
interface FocusAfter {
  name: string;
  /** The row's place among the rows shown when the change was made. */
  index: number;
  /** The row's control that had focus. */
  control: 'pin' | 'more';
  until: number;
}

const SectionView = memo(function SectionView({
  section,
  sectionCount,
  cap,
  collapsed,
  expanded,
  selected,
  current,
  tagsById,
  tags,
  groups,
  actions,
  onCollapse,
  onExpand,
}: {
  section: Section;
  sectionCount: number;
  cap: number;
  collapsed: boolean;
  expanded: boolean;
  selected: Set<string>;
  /** The section already is the list's filter, so its title links nowhere. */
  current: boolean;
  tagsById: Map<string, Tag>;
  tags: Tag[];
  groups: Group[];
  actions: ListActions;
  onCollapse: (key: string) => void;
  onExpand: (key: string) => void;
}) {
  const { key, title, sites } = section;
  const id = `sites-section-${key.replace(/[^a-z0-9-]/gi, '-')}`;
  const names = sites.map((site) => site.name);
  const chosen = names.filter((name) => selected.has(name)).length;
  const capped = names.length > maxSelection;
  const shown = shownSites(section, sectionCount, cap, expanded);
  const links = sites.reduce((sum, site) => sum + site.workingLinks, 0);
  const requests = sites.reduce((sum, site) => sum + weekRequests(site), 0);
  return (
    <Wire>
      <section className="sites-section" aria-labelledby={`${id}-title`}>
        <div className="sites-section-head">
          <button
            type="button"
            className="sites-section-toggle"
            aria-expanded={!collapsed}
            aria-controls={`${id}-rows`}
            aria-label={`${collapsed ? 'Expand' : 'Collapse'} ${title}`}
            onClick={() => onCollapse(key)}
          >
            <span aria-hidden="true">›</span>
          </button>
          <h2 id={`${id}-title`} className="sites-section-title">
            {section.slot !== undefined ? (
              <span
                className="wl-tag-dot"
                style={{ background: `var(--color-tag-${section.slot})` }}
                aria-hidden="true"
              />
            ) : null}
            {section.href && !current ? (
              <a href={section.href}>{title}</a>
            ) : (
              title
            )}
          </h2>
          <span className="sites-section-meta">
            <span>{plural(sites.length, 'site')}</span>
            <span>{plural(links, 'link')}</span>
            <span>{plural(requests, 'request')}</span>
            {section.note ? (
              <span className="sites-section-note">{section.note}</span>
            ) : null}
          </span>
          <Checkbox
            label={capped ? `Select the first ${maxSelection}` : 'Select all'}
            aria-label={
              capped
                ? `Select the first ${maxSelection} in ${title}`
                : `Select all in ${title}`
            }
            className="sites-section-select"
            checked={chosen === Math.min(names.length, maxSelection)}
            indeterminate={
              chosen > 0 && chosen < Math.min(names.length, maxSelection)
            }
            onChange={(event) => actions.select(names, event.target.checked)}
          />
        </div>
        <div id={`${id}-rows`} hidden={collapsed}>
          {collapsed ? null : (
            <>
              <HubLayout label={`Sites in ${title}`}>
                {shown.map((site) => (
                  <SiteRow
                    key={site.id}
                    site={site}
                    selected={selected.has(site.name)}
                    tagsById={tagsById}
                    tags={tags}
                    groups={groups}
                    actions={actions}
                  />
                ))}
              </HubLayout>
              {sectionCount > 1 && sites.length > cap ? (
                // One button for both, so focus stays on it.
                <Button
                  type="button"
                  className="sites-show-all"
                  onClick={() => onExpand(key)}
                >
                  {expanded
                    ? `Show fewer in ${title}`
                    : `Show all ${sites.length} in ${title}`}
                </Button>
              ) : null}
            </>
          )}
        </div>
      </section>
    </Wire>
  );
});

/**
 * `/sites`: the owner's sites in sections on wires, filtered by the
 * navigator and the search, with multi-select and bulk organising. Every
 * filter and view setting lives in the address.
 */
export function SitesPage({ api }: { api: ApiClient }) {
  const { params } = useLocation();
  const query = useMemo(() => readQuery(params), [params]);
  const list = useSites(api, query);
  const meta = useMeta(api);
  const flash = useFlash();
  const wide = useSyncExternalStore(subscribeWide, wideSnapshot);
  const cap = wide ? 5 : 3;
  const title = listTitle(query, meta.state);
  useTitle(title);

  const go = useCallback(
    (next: ListQuery, replace = false) =>
      navigate(queryHref(next), { replace }),
    [],
  );

  // The search field sends what is typed once typing pauses; a change to
  // the address from elsewhere (a chip, the navigator) replaces it.
  const search = useRef<HTMLInputElement>(null);
  const [text, setText] = useState(query.q);
  const [sent, setSent] = useState(query.q);
  if (query.q !== sent) {
    setSent(query.q);
    setText(query.q);
  }
  useEffect(() => {
    if (text.trim() === query.q.trim()) return;
    const timer = setTimeout(
      () => go({ ...query, q: text }, true),
      searchDelay,
    );
    return () => clearTimeout(timer);
  }, [text, query, go]);
  useSlashFocus(search);

  const ready = list.state.kind === 'ready' ? list.state.sites : undefined;
  const metaReady = meta.state.kind === 'ready' ? meta.state : undefined;
  const groups = useMemo(() => metaReady?.groups ?? [], [metaReady]);
  const tags = useMemo(() => metaReady?.tags ?? [], [metaReady]);
  const tagsById = useMemo(
    () => new Map(tags.map((tag) => [tag.id, tag])),
    [tags],
  );

  const sections = useMemo(
    () =>
      ready
        ? buildSections(
            ready,
            query.by,
            groups,
            tags,
            title === 'Sites' ? 'All sites' : title,
            { by: query.by, sort: query.sort, d: query.d },
          )
        : [],
    [ready, query.by, query.sort, query.d, groups, tags, title],
  );

  // Selection, by name. A site the filters stop showing leaves it, so a
  // change never reaches a site the owner cannot see in the list.
  const [selected, setChosen] = useState<Set<string>>(() => new Set());
  const [prunedFor, setPrunedFor] = useState(ready);
  if (ready && ready !== prunedFor) {
    setPrunedFor(ready);
    const shown = new Set(ready.map((site) => site.name));
    if ([...selected].some((name) => !shown.has(name)))
      setChosen(new Set([...selected].filter((name) => shown.has(name))));
  }
  const targets = useMemo(
    () => (ready ?? []).filter((site) => selected.has(site.name)),
    [ready, selected],
  );

  const [collapsed, setCollapsed] = useState<Set<string>>(() => new Set());
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set());
  const onCollapse = useCallback(
    (key: string) => setCollapsed((current) => toggled(current, key)),
    [],
  );
  const onExpand = useCallback(
    (key: string) => setExpanded((current) => toggled(current, key)),
    [],
  );

  const [toast, setToast] = useState<ToastState | null>(null);
  const toastKey = useRef(0);
  const say = (
    message: string,
    undo?: OrganiseRequest[],
    tone: ToastState['tone'] = 'info',
  ) => setToast({ message, key: ++toastKey.current, tone, undo });

  // Changes run one after another, in the order the owner made them; a
  // change that throws is reported and the next still runs.
  const queue = useRef(Promise.resolve());
  const enqueue = (task: () => Promise<void>) => {
    queue.current = queue.current
      .then(task)
      .catch(() => failed('make the change', ''));
  };
  const latest = useRef({ tags, list, meta });
  useLayoutEffect(() => {
    latest.current = { tags, list, meta };
  });
  const tagOrder = (ids: string[]) => {
    const names = new Map(latest.current.tags.map((t) => [t.id, t.name]));
    return ids.toSorted((a, b) =>
      (names.get(a) ?? '') < (names.get(b) ?? '') ? -1 : 1,
    );
  };
  const refresh = () => {
    latest.current.list.reload();
    latest.current.meta.reload();
    sitesChanged();
  };

  const post = async (change: OrganiseRequest) => {
    try {
      const { data, error } = await api.POST('/api/v0/organise', {
        body: change,
      });
      return data ?? error?.message ?? '';
    } catch {
      return '';
    }
  };

  const failed = (what: string, message: string) =>
    say(
      message
        ? `Could not ${what}: ${message}`
        : `Could not ${what}. Check your connection, then try again.`,
      undefined,
      'danger',
    );

  const organise = (change: OrganiseRequest, done: string, what: string) => {
    enqueue(async () => {
      const result = await post(change);
      if (typeof result === 'string') return failed(what, result);
      latest.current.list.patch((sites) =>
        applyChange(sites, change, tagOrder),
      );
      const undo = inverse(change, result.prior);
      say(done, undo.length ? undo : undefined);
      refresh();
    });
  };

  const undo = (requests: OrganiseRequest[]) => {
    enqueue(async () => {
      for (const request of requests) {
        const result = await post(request);
        if (typeof result === 'string') {
          refresh();
          return failed('undo the change', result);
        }
        latest.current.list.patch((sites) =>
          applyChange(sites, request, tagOrder),
        );
      }
      say('Undone.');
      refresh();
    });
  };

  const create = async (
    path: '/api/v0/tags' | '/api/v0/groups',
    name: string,
  ) => {
    try {
      const { data, error } = await api.POST(path, { body: { name } });
      return data ?? error?.message ?? '';
    } catch {
      return '';
    }
  };

  const tagChange = (names: string[], tag: Tag, add: boolean) =>
    organise(
      add
        ? { sites: names, addTagIds: [tag.id] }
        : { sites: names, removeTagIds: [tag.id] },
      add
        ? `Added ${tag.name} to ${siteCount(names)}`
        : `Removed ${tag.name} from ${siteCount(names)}`,
      add ? 'add the tag' : 'remove the tag',
    );
  const moveChange = (all: string[], group: Group | undefined) => {
    // Sites already there are left out; with none left, nothing happens.
    const current = latest.current.list.state;
    const where = new Map(
      current.kind === 'ready'
        ? current.sites.map((site) => [site.name, site.groupId])
        : [],
    );
    const names = all.filter((name) => where.get(name) !== group?.id);
    if (names.length === 0) return;
    organise(
      group
        ? { sites: names, groupId: group.id }
        : { sites: names, ungroup: true },
      group
        ? `Moved ${siteCount(names)} to ${group.name}`
        : `Took ${siteCount(names)} out of ${names.length === 1 ? 'its' : 'their'} group`,
      'move the sites',
    );
  };

  // A change to one row can move it to another section or out of the list,
  // taking focus with it. Focus then goes to the row where it is now, else
  // the row that took its place, else the search.
  const focusAfter = useRef<FocusAfter | null>(null);
  const remember = (names: string[]) => {
    const row = document.activeElement?.closest<HTMLElement>('[data-site]');
    if (names.length !== 1 || !row || row.dataset.site !== names[0]) return;
    const rows = [...document.querySelectorAll('[data-site]')];
    focusAfter.current = {
      name: names[0]!,
      index: rows.indexOf(row),
      control: document.activeElement?.matches('.sites-pin') ? 'pin' : 'more',
      until: Date.now() + 5000,
    };
  };
  useLayoutEffect(() => {
    const after = focusAfter.current;
    if (!after) return;
    if (Date.now() > after.until) {
      focusAfter.current = null;
      return;
    }
    const active = document.activeElement;
    if (active && active !== document.body && active.isConnected) return;
    const rows = [...document.querySelectorAll<HTMLElement>('[data-site]')];
    const same = rows.find((row) => row.dataset.site === after.name);
    const target = same
      ? same.querySelector<HTMLElement>(
          after.control === 'pin' ? '.sites-pin' : '.sites-row-more',
        )
      : (
          rows[after.index] ?? rows[after.index - 1]
        )?.querySelector<HTMLElement>('.sites-row-name');
    (target ?? search.current)?.focus();
  });

  // One object for every row, reading the latest handlers when called.
  const handlers = useRef<ListActions>(null!);
  useLayoutEffect(() => {
    handlers.current = {
      select: (names, on) =>
        setChosen((current) => {
          const next = new Set(current);
          for (const name of names) {
            if (!on) next.delete(name);
            else if (next.size < maxSelection) next.add(name);
          }
          return next;
        }),
      pin: (names, pinned) => {
        remember(names);
        organise(
          { sites: names, pinned },
          `${pinned ? 'Pinned' : 'Unpinned'} ${siteCount(names)}`,
          pinned ? 'pin the sites' : 'unpin the sites',
        );
      },
      tag: (names, tag, add) => {
        remember(names);
        tagChange(names, tag, add);
      },
      createTag: (names, name) => {
        enqueue(async () => {
          const tag = await create('/api/v0/tags', name);
          if (typeof tag === 'string') return failed('create the tag', tag);
          latest.current.meta.reload();
          tagChange(names, tag as Tag, true);
        });
      },
      move: (names, group) => {
        remember(names);
        moveChange(names, group);
      },
      createGroup: (names, name) => {
        enqueue(async () => {
          const group = await create('/api/v0/groups', name);
          if (typeof group === 'string')
            return failed('create the group', group);
          latest.current.meta.reload();
          moveChange(names, group as Group);
        });
      },
      copy: (site) => {
        navigator.clipboard.writeText(site.url).then(
          () => say(`Copied ${site.url}`),
          () => failed('copy the address', 'this browser did not allow it'),
        );
      },
    };
  });

  const actions = useMemo<ListActions>(
    () => ({
      select: (...a) => handlers.current.select(...a),
      pin: (...a) => handlers.current.pin(...a),
      tag: (...a) => handlers.current.tag(...a),
      createTag: (...a) => handlers.current.createTag(...a),
      move: (...a) => handlers.current.move(...a),
      createGroup: (...a) => handlers.current.createGroup(...a),
      copy: (...a) => handlers.current.copy(...a),
    }),
    [],
  );

  const [bulk, setBulk] = useState<'tags' | 'group' | 'pin' | null>(null);
  const bulkOpen = (which: 'tags' | 'group' | 'pin') => (open: boolean) =>
    setBulk(open ? which : null);
  const names = targets.map((site) => site.name);
  // Selected sites in a collapsed section or behind Show all.
  const unseen = useMemo(() => {
    const seen = new Set<string>();
    for (const section of sections)
      if (!collapsed.has(section.key))
        for (const site of shownSites(
          section,
          sections.length,
          cap,
          expanded.has(section.key),
        ))
          seen.add(site.name);
    return targets.filter((site) => !seen.has(site.name)).length;
  }, [sections, collapsed, expanded, cap, targets]);
  const bulkNote = [
    targets.length >= maxSelection
      ? `${maxSelection} is the most at once`
      : null,
    unseen ? `${unseen} not shown` : null,
  ]
    .filter(Boolean)
    .join(' · ');

  const chips = filterChips(query, meta.state);
  const isFiltered = filtered(query);
  const all = metaReady?.counts.all;
  const description =
    ready === undefined
      ? undefined
      : isFiltered && all !== undefined
        ? `${ready.length} of ${plural(all, 'site')}`
        : plural(ready.length, 'site');
  const currentKey = query.g
    ? `g:${query.g}`
    : query.t
      ? `t:${query.t}`
      : query.v === 'untagged'
        ? 't:none'
        : undefined;

  return (
    <div
      className={cx('sites-page', targets.length ? 'sites-page-bulk' : null)}
    >
      <PageHeader title={title} description={description} />
      {flash ? <Notice>{flash}</Notice> : null}
      <div className="sites-tools">
        <div className="sites-search">
          <label htmlFor="sites-search" className="wl-visually-hidden">
            Search sites
          </label>
          <input
            ref={search}
            id="sites-search"
            type="search"
            className="wl-input"
            value={text}
            placeholder="Search, or try tag:client group:docs is:public"
            autoComplete="off"
            spellCheck={false}
            aria-keyshortcuts="/"
            maxLength={200}
            onChange={(event) => setText(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter') go({ ...query, q: text }, true);
            }}
          />
          <kbd className="wl-kbd sites-search-key" aria-hidden="true">
            /
          </kbd>
        </div>
        <SelectField
          label="Group by"
          className="sites-tool"
          value={query.by}
          onChange={(event) =>
            go({ ...query, by: event.target.value as GroupBy }, true)
          }
        >
          {groupings.map((option) => (
            <option key={option.value} value={option.value}>
              {option.label}
            </option>
          ))}
        </SelectField>
        <SelectField
          label="Sort"
          className="sites-tool"
          value={query.sort}
          onChange={(event) =>
            go({ ...query, sort: event.target.value as SiteSort }, true)
          }
        >
          {sorts.map((option) => (
            <option key={option.value} value={option.value}>
              {option.label}
            </option>
          ))}
        </SelectField>
        <SegmentedControl
          label="Density"
          value={query.d}
          options={densities}
          onChange={(d) => go({ ...query, d }, true)}
        />
      </div>
      {chips.length ? (
        <div className="sites-chips" role="group" aria-label="Filters">
          {chips.map((chip) => (
            <span key={chip.key} className="sites-chip">
              {chip.label}
              <button
                type="button"
                className="sites-chip-remove"
                aria-label={`Remove filter ${chip.label}`}
                onClick={() => {
                  setText(chip.without.q);
                  go(chip.without);
                }}
              >
                <span aria-hidden="true">×</span>
              </button>
            </span>
          ))}
          <button
            type="button"
            className="sites-clear"
            onClick={() => {
              setText('');
              go(unfiltered(query));
            }}
          >
            Clear all
          </button>
        </div>
      ) : null}

      {list.state.kind === 'loading' ? (
        <p role="status">Loading sites…</p>
      ) : list.state.kind === 'error' ? (
        list.state.missing ? (
          <EmptyState title="No such group or tag">
            <p>
              The group or tag in this address is not one of yours, or was
              deleted.
            </p>
            <Button
              type="button"
              onClick={() => {
                setText('');
                go(unfiltered(query));
              }}
            >
              Clear filters
            </Button>
          </EmptyState>
        ) : (
          <div className="sites-error">
            <Notice tone="danger">
              Could not load your sites. Check your connection, then try again.
            </Notice>
            <Button type="button" onClick={list.retry}>
              Try again
            </Button>
          </div>
        )
      ) : ready && ready.length === 0 ? (
        isFiltered ? (
          <EmptyState title="No sites match">
            <p>
              Search looks at names, addresses, domains, tags and groups. Try
              removing a filter.
            </p>
            <Button
              type="button"
              onClick={() => {
                setText('');
                go(unfiltered(query));
              }}
            >
              Clear filters
            </Button>
          </EmptyState>
        ) : (
          <EmptyState title="No sites yet">
            <p>
              Your sites are listed here, with who can open each one and its
              live deploy.
            </p>
          </EmptyState>
        )
      ) : (
        <div
          className={cx(
            'sites-list',
            query.d === 'compact' ? 'sites-list-compact' : null,
          )}
          aria-busy={list.pending}
        >
          {meta.state.kind === 'error' ? (
            <p className="sites-meta-error" role="status">
              Could not load group and tag names.{' '}
              <button
                type="button"
                className="sites-link-button"
                onClick={meta.retry}
              >
                Load them again
              </button>
            </p>
          ) : null}
          {query.by === 'tag' ? (
            <p className="sites-note">
              A site with several tags appears under each of them.
            </p>
          ) : null}
          <div className="sites-columns" aria-hidden="true">
            <span />
            <span>Site</span>
            <span>Tags</span>
            <span>Who can open it</span>
            <span>Links</span>
            <span>7-day requests</span>
            <span>Live deploy</span>
            <span />
          </div>
          <HubLayout
            hub={
              <span className="sites-hub">
                <b className="sites-mono">wardlockd</b>
                <span>{description}</span>
              </span>
            }
            label="Sections"
          >
            {sections.map((section) => (
              <SectionView
                key={section.key}
                section={section}
                sectionCount={sections.length}
                cap={cap}
                collapsed={collapsed.has(section.key)}
                expanded={expanded.has(section.key)}
                selected={selected}
                current={section.key === currentKey}
                tagsById={tagsById}
                tags={tags}
                groups={groups}
                actions={actions}
                onCollapse={onCollapse}
                onExpand={onExpand}
              />
            ))}
          </HubLayout>
        </div>
      )}

      <BulkBar
        label="Bulk actions"
        count={targets.length}
        note={bulkNote}
        onClear={() => setChosen(new Set())}
      >
        <Popover
          label={`Tags for ${siteCount(names)}`}
          open={bulk === 'tags'}
          onOpenChange={bulkOpen('tags')}
          placement="above"
          trigger={(props) => (
            <button {...props} type="button" className="wl-bulk-action">
              Tags
            </button>
          )}
        >
          <TagsPicker
            targets={targets}
            tags={tags}
            onToggle={(tag, add) => tagChange(names, tag, add)}
            onCreate={(name) => actions.createTag(names, name)}
          />
        </Popover>
        <Popover
          label={`Move ${siteCount(names)} to a group`}
          open={bulk === 'group'}
          onOpenChange={bulkOpen('group')}
          placement="above"
          trigger={(props) => (
            <button {...props} type="button" className="wl-bulk-action">
              Move to group
            </button>
          )}
        >
          <GroupPicker
            targets={targets}
            groups={groups}
            onMove={(group) => {
              setBulk(null);
              moveChange(names, group);
            }}
            onCreate={(name) => {
              setBulk(null);
              actions.createGroup(names, name);
            }}
          />
        </Popover>
        <Popover
          label={`Pin ${siteCount(names)}`}
          open={bulk === 'pin'}
          onOpenChange={bulkOpen('pin')}
          placement="above"
          trigger={(props) => (
            <button {...props} type="button" className="wl-bulk-action">
              Pin
            </button>
          )}
        >
          <PinPicker
            targets={targets}
            onPin={(pinned) => {
              setBulk(null);
              actions.pin(names, pinned);
            }}
          />
        </Popover>
      </BulkBar>
      <Toast
        message={toast?.message ?? null}
        toastKey={toast?.key ?? 0}
        tone={toast?.tone ?? 'info'}
        {...(toast?.undo ? { onUndo: () => undo(toast.undo!) } : {})}
        onDismiss={() => setToast(null)}
      />
    </div>
  );
}
