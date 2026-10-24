// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient } from '@wardlock/apiclient-ts';
import {
  Button,
  cx,
  EmptyState,
  Notice,
  PageHeader,
  StatusTag,
} from '@wardlock/ui';
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type FocusEvent,
  type PointerEvent,
  type ReactNode,
  type RefObject,
} from 'react';
import { useTitle } from '../shell/page.js';
import { navigate, useLocation } from '../shell/router.js';
import { AccessWord } from './format.js';
import { useMapSites } from './map-data.js';
import {
  defaultWidth,
  dotCap,
  layoutMap,
  maxDot,
  minDot,
  serverRadius,
  type MapDot,
  type MapHub,
  type MapLayout,
} from './map-layout.js';
import { mapHref, ViewSwitch } from './map-switch.js';
import { filterChips, useSlashFocus } from './sites-page.js';
import { useMeta } from './sites-page-data.js';
import { readQuery, unfiltered, type ListQuery } from './sites-page-query.js';
import { weekRequests, type SiteSummary } from './sites-page-sections.js';
import './sites.css';
import './sites-page.css';

/** How long typing pauses before the search is sent. */
const searchDelay = 250;

const number = (n: number) => new Intl.NumberFormat('en-NZ').format(n);
const plural = (n: number, one: string, many = `${one}s`) =>
  `${number(n)} ${n === 1 ? one : many}`;

const accessWords = {
  protected: 'Protected',
  public: 'Public',
} as const;

/** A dot's accessible name: everything its tooltip says. */
export function dotName(dot: MapDot) {
  const { site } = dot;
  return [
    `${site.name}: ${accessWords[site.visibility]}`,
    site.unavailable ? 'taken down' : null,
    plural(dot.requests, 'request') + ' in 7 days',
    site.pinned ? 'pinned' : null,
    dot.dim ? 'outside the filters' : null,
  ]
    .filter(Boolean)
    .join(', ');
}

/** A hub's accessible name. */
export function hubName(hub: MapHub, filtering: boolean) {
  return [
    `${hub.title}: ${plural(hub.sites.length, 'site')}`,
    filtering ? `${number(hub.matching)} matching` : null,
  ]
    .filter(Boolean)
    .join(', ');
}

const siteHref = (site: SiteSummary) =>
  `/sites/${encodeURIComponent(site.name)}`;

/** The map filtered to one group, keeping how the list is drawn. */
const hubHref = (hub: MapHub, query: ListQuery) =>
  mapHref({ ...unfiltered(query), g: hub.group });

/**
 * A site's mark: a diamond, like the ward, when protected and a circle when
 * public, so access never rests on colour alone; taken down keeps the shape
 * as a dashed outline. A diamond has the area of the circle of radius `r`.
 */
export function DotMark({
  visibility,
  down,
  x,
  y,
  r,
  className,
}: {
  visibility: SiteSummary['visibility'];
  down: boolean;
  x: number;
  y: number;
  r: number;
  className?: string;
}) {
  const classes = cx(
    'map-dot',
    `map-dot-${visibility}`,
    down ? 'map-dot-down' : null,
    className,
  );
  if (visibility === 'public')
    return <circle className={classes} cx={x} cy={y} r={r} />;
  const h = round1(r * 1.2533);
  return (
    <polygon
      className={classes}
      points={`${x},${round1(y - h)} ${round1(x + h)},${y} ${x},${round1(y + h)} ${round1(x - h)},${y}`}
    />
  );
}

const round1 = (n: number) => Math.round(n * 10) / 10;

interface Tip {
  key: string;
  x: number;
  y: number;
  below: boolean;
  content: ReactNode;
}

function SiteTip({ dot }: { dot: MapDot }) {
  const { site } = dot;
  return (
    <>
      <b className="map-tip-title">{site.name}</b>
      <span className="map-tip-row">
        <AccessWord policy={site} />
        {site.unavailable ? (
          <StatusTag tone="danger">Taken down</StatusTag>
        ) : null}
      </span>
      <span>{plural(dot.requests, 'request')} in 7 days</span>
      {site.pinned ? <span>Pinned</span> : null}
      {dot.dim ? <span>Outside the filters</span> : null}
    </>
  );
}

function HubTip({ hub, filtering }: { hub: MapHub; filtering: boolean }) {
  return (
    <>
      <b className="map-tip-title">{hub.title}</b>
      <span>
        {plural(hub.sites.length, 'site')}
        {hub.protectedSites ? ` · ${number(hub.protectedSites)} protected` : ''}
      </span>
      <span>{plural(hub.requests, 'request')} in 7 days</span>
      {filtering ? <span>{number(hub.matching)} match the filters</span> : null}
      {hub.hidden ? (
        <span>
          {number(hub.dots.length)} drawn; Sites by group lists them all
        </span>
      ) : null}
      <span className="map-tip-hint">Select to show only this group</span>
    </>
  );
}

/**
 * The picture: wires, hubs and dots in SVG, with a tooltip for whichever
 * link is hovered or focused. Escape hides it.
 */
function MapPicture({
  layout,
  query,
  filtering,
}: {
  layout: MapLayout;
  query: ListQuery;
  filtering: boolean;
}) {
  const frame = useRef<HTMLDivElement>(null);
  const scroller = useRef<HTMLDivElement>(null);
  const [tip, setTip] = useState<Tip | null>(null);

  // A picture wider than the frame (a cluster too big for a phone) starts
  // with the server in the middle.
  useLayoutEffect(() => {
    const el = scroller.current;
    if (el) el.scrollLeft = (el.scrollWidth - el.clientWidth) / 2;
  }, [layout.width]);

  const show = (
    key: string,
    content: ReactNode,
    event: PointerEvent<Element> | FocusEvent<Element>,
  ) => {
    const box = frame.current?.getBoundingClientRect();
    const mark = event.currentTarget.getBoundingClientRect();
    if (!box) return;
    const below = mark.top - box.top < 96;
    setTip({
      key,
      content,
      below,
      x: Math.min(
        Math.max(mark.left + mark.width / 2 - box.left, 96),
        Math.max(box.width - 96, 96),
      ),
      y: below ? mark.bottom - box.top : mark.top - box.top,
    });
  };
  const hide = (key: string) =>
    setTip((current) => (current?.key === key ? null : current));

  // Escape hides the tooltip: from the map, or from anywhere while the
  // pointer shows it and nothing else has focus.
  useEffect(() => {
    if (!tip) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== 'Escape' || event.defaultPrevented) return;
      const target = event.target as Node | null;
      if (
        target === document.body ||
        (target && frame.current?.contains(target))
      )
        setTip(null);
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [tip]);

  const handlers = (key: string, content: ReactNode) => ({
    onPointerEnter: (event: PointerEvent<Element>) => show(key, content, event),
    onPointerLeave: () => hide(key),
    onFocus: (event: FocusEvent<Element>) => show(key, content, event),
    onBlur: () => hide(key),
  });

  const { server, metrics } = layout;
  return (
    <div className="map-frame" ref={frame}>
      <div className="map-scroll" ref={scroller}>
        <svg
          className="map-svg"
          // Drawn at the size it was laid out for, never scaled, so its text
          // keeps the stylesheet's sizes.
          width={layout.width}
          height={layout.height}
          viewBox={`0 0 ${layout.width} ${layout.height}`}
          role="group"
          aria-label={`Map of ${plural(layout.sites, 'site')} in ${plural(layout.hubs.length, 'group')} around wardlockd`}
        >
          <g aria-hidden="true">
            {layout.hubs.map((hub) => (
              <g key={hub.key} className={cx(hub.dim ? 'map-dim' : null)}>
                <polyline
                  className="map-wire"
                  points={hub.wire.map((p) => `${p.x},${p.y}`).join(' ')}
                />
                {hub.dots.map((dot) => (
                  <line
                    key={dot.site.id}
                    className={cx(
                      'map-spoke',
                      dot.access === 'public' ? 'map-spoke-public' : null,
                      dot.dim && !hub.dim ? 'map-dim' : null,
                    )}
                    x1={hub.x}
                    y1={hub.y}
                    x2={dot.x}
                    y2={dot.y}
                  />
                ))}
              </g>
            ))}
            {layout.hubs.map((hub) =>
              hub.ward ? (
                <rect
                  key={hub.key}
                  className={cx('map-ward', hub.dim ? 'map-dim' : null)}
                  x={hub.ward.x - 5}
                  y={hub.ward.y - 5}
                  width={10}
                  height={10}
                  transform={`rotate(45 ${hub.ward.x} ${hub.ward.y})`}
                />
              ) : null,
            )}
          </g>
          {layout.hubs.map((hub) => (
            <g key={hub.key}>
              <a
                href={hubHref(hub, query)}
                className={cx('map-hub', hub.dim ? 'map-dim' : null)}
                aria-label={hubName(hub, filtering)}
                {...handlers(
                  hub.key,
                  <HubTip hub={hub} filtering={filtering} />,
                )}
              >
                <circle
                  className={cx(
                    'map-hub-mark',
                    hub.group === 'none' ? 'map-hub-none' : null,
                  )}
                  cx={hub.x}
                  cy={hub.y}
                  r={metrics.hub}
                />
                <text className="map-hub-count" x={hub.x} y={hub.y + 4}>
                  {number(hub.sites.length)}
                </text>
                <text
                  className="map-hub-title"
                  x={hub.titleAt.x}
                  y={hub.titleAt.y}
                >
                  {hub.titleText}
                </text>
              </a>
              {hub.dots.map((dot) => (
                <a
                  key={dot.site.id}
                  href={siteHref(dot.site)}
                  className={cx('map-site', dot.dim ? 'map-dim' : null)}
                  aria-label={dotName(dot)}
                  data-access={dot.access}
                  data-r={dot.r}
                  {...handlers(`s:${dot.site.id}`, <SiteTip dot={dot} />)}
                >
                  <circle
                    className="map-hit"
                    cx={dot.x}
                    cy={dot.y}
                    r={Math.max(dot.r + 3, 10)}
                  />
                  <circle
                    className="map-focus"
                    cx={dot.x}
                    cy={dot.y}
                    r={dot.r + 4}
                  />
                  <DotMark
                    visibility={dot.site.visibility}
                    down={dot.site.unavailable}
                    x={dot.x}
                    y={dot.y}
                    r={dot.r}
                  />
                  {dot.label ? (
                    <text
                      className="map-label"
                      x={dot.label.x}
                      y={dot.label.y}
                      // A style: map.css centres text, over any attribute.
                      style={{ textAnchor: dot.label.anchor }}
                    >
                      {dot.label.text}
                    </text>
                  ) : null}
                </a>
              ))}
            </g>
          ))}
          <g className="map-server" aria-hidden="true">
            <circle cx={server.x} cy={server.y} r={serverRadius} />
            <text className="map-server-name" x={server.x} y={server.y - 2}>
              wardlockd
            </text>
            <text className="map-server-count" x={server.x} y={server.y + 14}>
              {plural(layout.sites, 'site')}
            </text>
          </g>
        </svg>
      </div>
      {tip ? (
        <div
          className={cx('map-tip', tip.below ? 'map-tip-below' : null)}
          style={{ left: tip.x, top: tip.y }}
          // Everything it says is in the link's name.
          aria-hidden="true"
        >
          {tip.content}
        </div>
      ) : null}
    </div>
  );
}

function Swatch({
  wide = false,
  children,
}: {
  wide?: boolean;
  children: ReactNode;
}) {
  return (
    <svg
      className={cx('map-swatch', wide ? 'map-swatch-wide' : null)}
      viewBox={wide ? '0 0 36 24' : '0 0 24 24'}
      aria-hidden="true"
      focusable="false"
    >
      {children}
    </svg>
  );
}

function Legend({
  layout,
  filtering,
}: {
  layout: MapLayout;
  filtering: boolean;
}) {
  return (
    <ul className="map-legend" aria-label="Legend">
      <li>
        <Swatch>
          <DotMark visibility="protected" down={false} x={12} y={12} r={6} />
        </Swatch>
        Protected
      </li>
      <li>
        <Swatch>
          <DotMark visibility="public" down={false} x={12} y={12} r={6} />
        </Swatch>
        Public
      </li>
      <li>
        <Swatch wide>
          <DotMark visibility="protected" down x={9} y={12} r={6} />
          <DotMark visibility="public" down x={27} y={12} r={6} />
        </Swatch>
        Dashed: taken down
      </li>
      <li>
        <Swatch>
          <rect
            className="map-ward"
            x={7}
            y={7}
            width={10}
            height={10}
            transform="rotate(45 12 12)"
          />
        </Swatch>
        Group has protected sites
      </li>
      <li>
        <Swatch wide>
          <circle className="map-dot map-dot-key" cx={6} cy={12} r={minDot} />
          <circle className="map-dot map-dot-key" cx={24} cy={12} r={maxDot} />
        </Swatch>
        Size: requests in 7 days
        {layout.maxRequests > 0
          ? ` (largest: ${number(layout.maxRequests)} or more)`
          : ''}
      </li>
      <li>Names on pinned sites</li>
      {filtering ? (
        <li>
          <Swatch>
            <DotMark
              visibility="protected"
              down={false}
              x={12}
              y={12}
              r={6}
              className="map-dim"
            />
          </Swatch>
          Faded: outside the filters
        </li>
      ) : null}
    </ul>
  );
}

/** The text alternative: every group and its sites, as lists. */
function SitesByGroup({
  layout,
  query,
  matches,
  summaryRef,
}: {
  layout: MapLayout;
  query: ListQuery;
  matches: Set<string> | undefined;
  summaryRef: RefObject<HTMLElement | null>;
}) {
  const filtering = matches !== undefined;
  // Below 48rem dots are smaller than a fingertip, so the list starts open.
  const [open, setOpen] = useState(
    () => window.matchMedia?.('(max-width: 47.99rem)').matches ?? false,
  );
  return (
    <details
      className="map-list"
      id="map-sites"
      open={open}
      onToggle={(event) => setOpen(event.currentTarget.open)}
    >
      <summary ref={summaryRef}>
        Sites by group <span>({number(layout.sites)})</span>
      </summary>
      <ul className="map-list-groups">
        {layout.hubs.map((hub) => (
          <li key={hub.key}>
            <span className="map-list-group">
              <a href={hubHref(hub, query)}>{hub.title}</a>
              <span className="map-list-meta">
                {plural(hub.sites.length, 'site')}
                {filtering ? ` · ${number(hub.matching)} matching` : ''}
              </span>
            </span>
            {hub.sites.length ? (
              <ul aria-label={`Sites in ${hub.title}`}>
                {hub.sites.map((site) => {
                  const requests = weekRequests(site);
                  return (
                    <li key={site.id}>
                      <a href={siteHref(site)} className="sites-mono">
                        {site.name}
                      </a>
                      <AccessWord policy={site} />
                      {site.unavailable ? (
                        <StatusTag tone="danger">Taken down</StatusTag>
                      ) : null}
                      <span className="map-list-meta">
                        {plural(requests, 'request')} in 7 days
                        {site.pinned ? ' · pinned' : ''}
                      </span>
                      {matches?.has(site.id) ? (
                        <StatusTag tone="primary">Matches</StatusTag>
                      ) : null}
                    </li>
                  );
                })}
              </ul>
            ) : null}
          </li>
        ))}
      </ul>
    </details>
  );
}

/** The element's content width, kept up to date; 0 until measured. */
function useWidth(element: HTMLElement | null) {
  const [width, setWidth] = useState(0);
  useLayoutEffect(() => {
    if (!element) return;
    const read = () => setWidth(Math.floor(element.clientWidth));
    read();
    if (typeof ResizeObserver === 'undefined') return;
    const observer = new ResizeObserver(read);
    observer.observe(element);
    return () => observer.disconnect();
  }, [element]);
  return width;
}

/**
 * `/sites?view=map`: every site around the server, by group, coloured by access
 * and sized by requests. The list's filters and search fade the sites they
 * leave out instead of hiding them.
 */
export function MapPage({ api }: { api: ApiClient }) {
  const { params } = useLocation();
  const query = useMemo(() => readQuery(params), [params]);
  const data = useMapSites(api, query);
  const meta = useMeta(api);
  useTitle('Sites map');

  const go = useCallback(
    (next: ListQuery, replace = false) => navigate(mapHref(next), { replace }),
    [],
  );

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

  const groups = useMemo(
    () => (meta.state.kind === 'ready' ? meta.state.groups : []),
    [meta.state],
  );
  const ready = data.state.kind === 'ready' ? data.state : undefined;
  const filtering = ready?.matches !== undefined;
  const [body, setBody] = useState<HTMLDivElement | null>(null);
  const width = useWidth(body);
  const layout = useMemo(
    () =>
      ready && meta.state.kind === 'ready'
        ? layoutMap(ready.sites, groups, ready.matches, {
            // Inside the frame's border.
            width: width ? width - 2 : defaultWidth,
          })
        : undefined,
    [ready, meta.state.kind, groups, width],
  );

  const summary = useRef<HTMLElement>(null);
  const chips = filterChips(query, meta.state);
  const clear = () => {
    setText('');
    go(unfiltered(query));
  };
  const description = ready
    ? ready.matches
      ? `${number(ready.matches.size)} of ${plural(ready.sites.length, 'site')} match`
      : plural(ready.sites.length, 'site')
    : undefined;
  const overflow = layout?.hubs.filter((hub) => hub.hidden > 0) ?? [];
  const openList = () => {
    const list = summary.current?.parentElement;
    if (list instanceof HTMLDetailsElement) list.open = true;
    summary.current?.focus();
  };

  return (
    <div className="sites-page map-page">
      <div className="map-head">
        <PageHeader title="Sites map" description={description} />
        <ViewSwitch current="map" query={query} />
      </div>
      <div className="sites-tools map-tools">
        <div className="sites-search">
          <label htmlFor="map-search" className="wl-visually-hidden">
            Search sites
          </label>
          <input
            ref={search}
            id="map-search"
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
          <button type="button" className="sites-clear" onClick={clear}>
            Clear all
          </button>
        </div>
      ) : null}

      {data.state.kind === 'error' ? (
        data.state.missing ? (
          <EmptyState title="No such group or tag">
            <p>
              The group or tag in this address is not one of yours, or was
              deleted.
            </p>
            <Button type="button" onClick={clear}>
              Clear filters
            </Button>
          </EmptyState>
        ) : (
          <div className="sites-error">
            <Notice tone="danger">
              Could not load your sites. Check your connection, then try again.
            </Notice>
            <Button type="button" onClick={data.retry}>
              Try again
            </Button>
          </div>
        )
      ) : meta.state.kind === 'error' ? (
        <div className="sites-error">
          <Notice tone="danger">
            Could not load your groups. Check your connection, then try again.
          </Notice>
          <Button type="button" onClick={meta.retry}>
            Try again
          </Button>
        </div>
      ) : !layout ? (
        <p role="status">Loading the map…</p>
      ) : layout.sites === 0 ? (
        <EmptyState title="No sites yet">
          <p>
            Your sites appear here around your server, grouped and coloured by
            who can open them.
          </p>
        </EmptyState>
      ) : (
        <div className="map-body" aria-busy={data.pending} ref={setBody}>
          {filtering && ready?.matches?.size === 0 ? (
            <p className="map-note" role="status">
              No sites match. Search looks at names, addresses, domains, tags
              and groups.
            </p>
          ) : null}
          <a
            className="map-skip"
            href="#map-sites"
            onClick={(event) => {
              event.preventDefault();
              openList();
            }}
          >
            Skip the map
          </a>
          <MapPicture layout={layout} query={query} filtering={filtering} />
          {overflow.length ? (
            <p className="map-note">
              A group draws at most {number(dotCap)} dots, pinned and busiest
              first. Not drawn:{' '}
              {overflow
                .map(
                  (hub) =>
                    `${hub.title} ${number(hub.hidden)} more` +
                    (filtering
                      ? `, ${number(hub.hiddenMatching)} matching`
                      : ''),
                )
                .join('; ')}
              :{' '}
              <a
                href="#map-sites"
                onClick={(event) => {
                  event.preventDefault();
                  openList();
                }}
              >
                see the list
              </a>
              .
            </p>
          ) : null}
          <Legend layout={layout} filtering={filtering} />
          <SitesByGroup
            layout={layout}
            query={query}
            matches={ready?.matches}
            summaryRef={summary}
          />
        </div>
      )}
    </div>
  );
}
