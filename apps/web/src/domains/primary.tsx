// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient } from '@wardlock/apiclient-ts';
import { Button, Card, Fieldset, Notice, Radio } from '@wardlock/ui';
import { useState, type FormEvent } from 'react';
import type { Site } from '../sites/format.js';
import type { Domain } from './format.js';

/**
 * The owner's choice of primary hostname and whether it is in effect. A
 * chosen custom domain is in effect only while it is active: while it is
 * lapsed nothing redirects (the site's `primaryHost` is absent) until it
 * checks out again.
 */
export function primaryOf(site: Site, domains: Domain[]) {
  const domain = domains.find((d) => d.primary);
  if (domain)
    return {
      chosen: domain.hostname,
      effective: domain.status === 'active' ? domain.hostname : undefined,
    };
  return { chosen: site.primaryHost, effective: site.primaryHost };
}

/** The served hostnames that redirect to `primary`: all the others. */
export function redirectingTo(primary: string, site: Site, domains: Domain[]) {
  return [
    site.hostname,
    ...domains.filter((d) => d.status === 'active').map((d) => d.hostname),
  ].filter((hostname) => hostname !== primary);
}

/** A hostname as a list item's text. */
function HostList({ hosts }: { hosts: string[] }) {
  return (
    <ul className="domain-primary-hosts" aria-label="Redirecting hostnames">
      {hosts.map((host) => (
        <li key={host}>
          <code>{host}</code>
        </li>
      ))}
    </ul>
  );
}

/**
 * What the primary hostname does now: which hostnames redirect to it, or
 * that nothing redirects and why.
 */
function PrimarySummary({ site, domains }: { site: Site; domains: Domain[] }) {
  const { chosen, effective } = primaryOf(site, domains);
  if (chosen && !effective)
    return (
      <Notice tone="warning">
        <code>{chosen}</code> is the primary hostname, but it has lapsed, so
        nothing redirects: every hostname serves the site until its DNS record
        checks out again. Choose another primary, or none, to stop waiting.
      </Notice>
    );
  if (!effective)
    return <p>No primary hostname: every hostname serves the site.</p>;
  const others = redirectingTo(effective, site, domains);
  return (
    <div className="domain-primary-summary">
      <p>
        <code>{effective}</code> is the primary hostname.{' '}
        {others.length === 0
          ? 'The site has no other hostname to redirect yet.'
          : 'Requests to these hostnames redirect to it, keeping the path and query:'}
      </p>
      {others.length > 0 && <HostList hosts={others} />}
    </div>
  );
}

/** What each choice means, under its label. */
function hint(
  hostname: string,
  domain: Domain | undefined,
  effective: string | undefined,
) {
  if (domain?.status === 'pending')
    return 'Pending: it can be the primary once its DNS record checks out.';
  if (domain?.status === 'lapsed')
    return domain.primary
      ? 'Lapsed: not served, so nothing redirects to it until it checks out again.'
      : 'Lapsed: it can be the primary once its DNS record checks out again.';
  if (hostname === effective)
    return 'The primary now. Every other hostname redirects here.';
  if (effective) return `Redirects to ${effective} now.`;
  return domain ? 'Custom domain.' : 'The site’s own address, always served.';
}

/**
 * Chooses the site's primary hostname, or none. Only a served hostname can
 * be chosen: the site's own or an active custom domain. Preview addresses
 * are never redirected.
 */
export function PrimaryHostCard({
  api,
  site,
  domains,
  onSaved,
}: {
  api: ApiClient;
  site: Site;
  /** The custom domains loaded on the Domains tab. */
  domains: Domain[];
  /** The site as the server answered, after a change. */
  onSaved: (site: Site) => void;
}) {
  const { chosen, effective } = primaryOf(site, domains);
  const [choice, setChoice] = useState(chosen ?? '');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState<string | null>(null);
  const options: { hostname: string; domain?: Domain }[] = [
    { hostname: site.hostname },
    ...domains.map((domain) => ({ hostname: domain.hostname, domain })),
  ];
  // A choice whose domain was removed meanwhile falls back to the saved one.
  const current =
    choice === '' || options.some((o) => o.hostname === choice)
      ? choice
      : (chosen ?? '');
  const unchanged = current === (chosen ?? '');

  const save = async (event: FormEvent) => {
    event.preventDefault();
    if (unchanged) return;
    setBusy(true);
    setError(null);
    setSaved(null);
    const params = { path: { site: site.name } };
    const result = await (
      current
        ? api.PUT('/api/v0/sites/{site}/primary-host', {
            params,
            body: { host: current },
          })
        : api.DELETE('/api/v0/sites/{site}/primary-host', { params })
    ).catch(() => null);
    setBusy(false);
    if (result?.data) {
      setSaved(
        current
          ? `Saved. ${current} is the primary hostname.`
          : 'Saved. Every hostname serves the site, with no redirects.',
      );
      return onSaved(result.data);
    }
    const code = result?.error?.code;
    setError(
      code === 'domain_not_active'
        ? `${current} is not active, so it cannot be the primary yet. Choose it once its DNS record checks out.`
        : code === 'not_found'
          ? `${current} is no longer one of this site’s hostnames. Reload the page to see its domains.`
          : result?.error?.message
            ? `Could not change the primary hostname: ${result.error.message}`
            : 'Could not change the primary hostname. Check your connection, then try again.',
    );
  };

  return (
    <Card title="Primary hostname">
      <p className="site-card-lead">
        Send visitors on every other hostname to one address, so search engines
        and bookmarks settle on it. Previews keep their own addresses.
      </p>
      <PrimarySummary site={site} domains={domains} />
      {site.unavailable ? (
        <p>
          While the operator has taken this site down, its primary hostname
          stays as it is.
        </p>
      ) : (
        <form className="domain-primary-form" onSubmit={save} noValidate>
          <Fieldset legend="Choose the primary">
            <Radio
              name="primary"
              value=""
              label="None"
              hint="Every hostname serves the site."
              checked={current === ''}
              onChange={() => setChoice('')}
            />
            {options.map(({ hostname, domain }) => (
              <Radio
                key={hostname}
                className="domain-primary-host"
                name="primary"
                value={hostname}
                label={hostname}
                hint={hint(hostname, domain, effective)}
                checked={current === hostname}
                // A hostname that is not served cannot become the primary;
                // the current choice stays selectable so it can be kept.
                disabled={
                  domain !== undefined &&
                  domain.status !== 'active' &&
                  hostname !== chosen
                }
                onChange={() => setChoice(hostname)}
              />
            ))}
          </Fieldset>
          {error && <Notice tone="danger">{error}</Notice>}
          <div role="status" className="domain-live">
            {saved && <div className="wl-notice wl-notice-info">{saved}</div>}
          </div>
          <div>
            <Button
              type="submit"
              variant="primary"
              disabled={busy || unchanged}
            >
              {busy ? 'Saving…' : 'Save primary hostname'}
            </Button>
          </div>
        </form>
      )}
    </Card>
  );
}
