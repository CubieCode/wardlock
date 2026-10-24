// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import { useLayoutEffect, useRef, useState } from 'react';
import { countryName } from './country.js';
import { countryPoints, land, mapHeight, mapWidth } from './world.gen.js';

export interface MapCountry {
  code: string;
  value: number;
  /** The tooltip's second line, such as "31 requests · 72%". */
  detail: string;
}

/** Bubbles labelled with their code: the busiest few. */
const labelled = 5;

/**
 * A bubble map of requests by country, centred on the Pacific: one bubble
 * per country, its area in proportion to its requests, at the country's
 * label point. With `server`, a hub in that country with a line to each
 * bubble; without it, no hub. Countries with no point (rare codes) are left
 * off the map; the list and table beside it still have them. Each bubble is
 * focusable and shows its tooltip on focus or hover; Escape hides it. The
 * tooltip is kept inside the map, flipping below a bubble near the top. The
 * legend above names the hub only when it is drawn.
 *
 * Loaded on demand (its geometry is about 55 KB), so only the analytics page
 * fetches it.
 */
export default function CountryMap({
  countries,
  server,
}: {
  countries: MapCountry[];
  server?: string | undefined;
}) {
  const [active, setActive] = useState<string | null>(null);
  const placed = countries.flatMap((country) => {
    const point = countryPoints[country.code];
    return point && country.value > 0 ? [{ ...country, point }] : [];
  });
  const max = Math.max(1, ...placed.map((c) => c.value));
  const radius = (value: number) => 3 + Math.sqrt(value / max) * 18;
  // Largest first, so smaller bubbles stay on top and reachable.
  const drawn = [...placed].sort((a, b) => b.value - a.value);
  const hub = server ? countryPoints[server] : undefined;
  const shown = drawn.find((c) => c.code === active);
  const tip = useRef<HTMLSpanElement>(null);

  // Keep the tooltip inside the map: centred over its bubble where it fits,
  // pushed in from an edge where it doesn't, and below the bubble when there
  // is no room above. Set before paint, so it never shows overflowing.
  useLayoutEffect(() => {
    const el = tip.current;
    const map = el?.parentElement;
    if (!el || !map || !shown) return;
    const scale = map.clientWidth / mapWidth;
    const [x, y] = shown.point;
    const r = radius(shown.value) * scale;
    const gap = 4;
    const left = Math.min(
      Math.max(x * scale - el.offsetWidth / 2, 0),
      Math.max(map.clientWidth - el.offsetWidth, 0),
    );
    const above = y * scale - r - gap - el.offsetHeight;
    el.style.left = `${left}px`;
    el.style.top = `${above >= 0 ? above : y * scale + r + gap}px`;
    el.style.visibility = 'visible';
  });

  return (
    <>
      <div className="country-legend" aria-hidden="true">
        <span>
          <i className="country-key-bubble" />
          Bubble area: requests
        </span>
        {hub ? (
          <span>
            <i className="country-key-hub" />
            Your server
          </span>
        ) : null}
      </div>
      <div className="country-map">
        <svg
          viewBox={`0 0 ${mapWidth} ${mapHeight}`}
          role="group"
          aria-label="Map of requests by country"
        >
          <path className="country-land" d={land} aria-hidden="true" />
          {hub ? (
            <g className="country-lines" aria-hidden="true">
              {drawn.map(({ code, point: [x, y] }) => (
                <line key={code} x1={hub[0]} y1={hub[1]} x2={x} y2={y} />
              ))}
            </g>
          ) : null}
          {drawn.map(({ code, value, detail, point: [x, y] }) => (
            <circle
              key={code}
              className="country-bubble"
              cx={x}
              cy={y}
              r={radius(value)}
              tabIndex={0}
              role="img"
              aria-label={`${countryName(code)}: ${detail}`}
              onFocus={() => setActive(code)}
              onBlur={() => setActive(null)}
              onPointerEnter={() => setActive(code)}
              onPointerLeave={() => setActive(null)}
              onKeyDown={(event) => {
                if (event.key === 'Escape') setActive(null);
              }}
            />
          ))}
          {hub && server ? (
            <circle
              className="country-hub"
              cx={hub[0]}
              cy={hub[1]}
              r={5}
              role="img"
              aria-label={`Your server, in ${countryName(server)}`}
            />
          ) : null}
        </svg>
        {/* HTML, not SVG text, so labels stay readable however small the map
          is drawn; hidden on narrow maps, where the list and tooltips name
          the countries. */}
        <div className="country-labels" aria-hidden="true">
          {drawn.slice(0, labelled).map(({ code, value, point: [x, y] }) => {
            const r = radius(value);
            const below = y < mapHeight * 0.75;
            return (
              <span
                key={code}
                className={below ? 'below' : 'above'}
                style={{
                  left: `${(x / mapWidth) * 100}%`,
                  top: `${((below ? y + r : y - r) / mapHeight) * 100}%`,
                }}
              >
                {code}
              </span>
            );
          })}
        </div>
        {shown ? (
          // Placed by the layout effect above; hidden until then.
          <span
            key={shown.code}
            ref={tip}
            role="tooltip"
            className="wl-tooltip country-tip"
            style={{ visibility: 'hidden' }}
          >
            <b>{countryName(shown.code)}</b>
            <span>{shown.detail}</span>
          </span>
        ) : null}
      </div>
    </>
  );
}
