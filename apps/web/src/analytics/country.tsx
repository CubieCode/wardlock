// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { components } from '@wardlock/apiclient-ts';
import './country.css';

export type RequestCountry = components['schemas']['RequestCountry'];
export type CountryLookup = components['schemas']['CountryLookup'];

const regionNames = new Intl.DisplayNames(['en-NZ'], {
  type: 'region',
  fallback: 'code',
});

/** A country's English name, or its code when the browser has no name. */
export function countryName(code: string) {
  try {
    return regionNames.of(code) ?? code;
  } catch {
    return code;
  }
}

/**
 * What `unknown` means, as SPEC words it: the country database has no
 * country for the request's IP.
 */
export const unknownMeaning =
  'The country database has no country for these: often a private network, a relay or a VPN’s exit that it doesn’t know.';

/** One access-log entry's country: its code and name, or Unknown. */
export function CountryCell({ country }: { country: RequestCountry }) {
  if (country === 'unknown')
    return <span className="visit-muted">Unknown</span>;
  return (
    <span className="country-cell">
      <abbr className="country-code" title={countryName(country)}>
        {country}
      </abbr>
      {countryName(country)}
    </span>
  );
}

const httpsUrl = /(https:\/\/[^\s<>"'()]+)/;

/**
 * The credit the country database's licence requires, exactly as the server
 * gives it, with each https URL in it as a link. Other schemes stay text.
 */
export function Attribution({ text }: { text: string }) {
  return (
    <p className="country-credit">
      {text.split(httpsUrl).map((part, i) => {
        if (i % 2 === 0) return part;
        // Sentence punctuation after a URL is not part of it.
        const url = part.replace(/[.,;:!?]+$/, '');
        const rest = part.slice(url.length);
        return (
          <span key={i}>
            <a href={url} target="_blank" rel="noopener noreferrer">
              {url}
            </a>
            {rest}
          </span>
        );
      })}
    </p>
  );
}
