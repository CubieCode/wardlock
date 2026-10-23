// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import { useState } from 'react';
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
 * focusable and shows its tooltip on focus or hover; Escape hides it.
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

  return (
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
        <g className="country-labels" aria-hidden="true">
          {drawn.slice(0, labelled).map(({ code, value, point: [x, y] }) => {
            const r = radius(value);
            const below = y < mapHeight * 0.75;
            return (
              <text
                key={code}
                x={x}
                y={below ? y + r + 13 : y - r - 5}
                textAnchor="middle"
              >
                {code}
              </text>
            );
          })}
        </g>
      </svg>
      {shown ? (
        <span
          role="tooltip"
          className="wl-tooltip country-tip"
          style={{
            left: `${(shown.point[0] / mapWidth) * 100}%`,
            top: `${((shown.point[1] - radius(shown.value)) / mapHeight) * 100}%`,
          }}
        >
          <b>{countryName(shown.code)}</b>
          <span>{shown.detail}</span>
        </span>
      ) : null}
    </div>
  );
}
