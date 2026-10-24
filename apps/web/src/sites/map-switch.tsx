// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import { listIcon } from '../shell/icons.js';
import { queryHref, type ListQuery } from './sites-page-query.js';
import './map.css';

/** Whether the sites page's address asks for the map. */
export const isMap = (params: URLSearchParams) => params.get('view') === 'map';

/** The map's address for this list state: the same filters and settings. */
export function mapHref(query: ListQuery) {
  const list = queryHref(query);
  return `${list}${list.includes('?') ? '&' : '?'}view=map`;
}

const mapIcon = (
  <svg
    viewBox="0 0 16 16"
    fill="none"
    stroke="currentColor"
    strokeWidth="1.5"
    strokeLinecap="round"
    focusable="false"
  >
    <circle cx="8" cy="8" r="2.25" />
    <circle cx="2.75" cy="3.25" r="1.25" />
    <circle cx="13.25" cy="3.25" r="1.25" />
    <circle cx="3.25" cy="13" r="1.25" />
    <circle cx="13" cy="12.75" r="1.25" />
    <path d="M6.3 6.5 3.7 4.1M9.7 6.5l2.6-2.4M6.4 9.6 4.1 12M9.6 9.6l2.5 2.4" />
  </svg>
);

/**
 * List or Map: two links that keep the filters, search and list settings,
 * with the current one marked.
 */
export function ViewSwitch({
  current,
  query,
}: {
  current: 'list' | 'map';
  query: ListQuery;
}) {
  return (
    <nav className="map-switch" aria-label="Show sites as">
      <a
        href={queryHref(query)}
        aria-current={current === 'list' ? 'page' : undefined}
      >
        <span className="map-switch-icon" aria-hidden="true">
          {listIcon}
        </span>
        List
      </a>
      <a
        href={mapHref(query)}
        aria-current={current === 'map' ? 'page' : undefined}
      >
        <span className="map-switch-icon" aria-hidden="true">
          {mapIcon}
        </span>
        Map
      </a>
    </nav>
  );
}
