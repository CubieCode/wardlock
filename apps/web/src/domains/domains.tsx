// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient } from '@wardlock/apiclient-ts';
import {
  Button,
  Card,
  CopyField,
  EmptyState,
  Notice,
  TextField,
  type NoticeTone,
} from '@wardlock/ui';
import { useEffect, useRef, useState, type FormEvent } from 'react';
import type { Site } from '../sites/format.js';
import {
  byHostname,
  CheckTimes,
  DomainStatus,
  failureText,
  pendingLifetime,
  statusSummary,
  type DnsRecord,
  type Domain,
} from './format.js';
import { RemoveDomainDialog } from './remove.js';
import './domains.css';

type ListState =
  | { kind: 'loading' }
  | { kind: 'error' }
  | { kind: 'ready'; domains: Domain[]; nextCursor: string | undefined };

/**
 * A site's custom domains: add a hostname, see the DNS record to create
 * (and the TXT fallback) with copy actions, follow each one's status and
 * last check, check it now, and remove it.
 */
export function DomainsCard({ api, site }: { api: ApiClient; site: Site }) {
  const [state, setState] = useState<ListState>({ kind: 'loading' });
  const [removing, setRemoving] = useState<Domain | null>(null);
  const [done, setDone] = useState<string | null>(null);
  const [more, setMore] = useState<'idle' | 'loading' | 'error'>('idle');
  const [attempt, setAttempt] = useState(0);
  // Bumped by every change, so an older page requested before it cannot
  // put back a removed domain.
  const generation = useRef(0);

  useEffect(() => {
    let live = true;
    api
      .GET('/api/v0/sites/{site}/domains', {
        params: { path: { site: site.name } },
      })
      .then(
        ({ data }) =>
          live &&
          setState(
            data
              ? {
                  kind: 'ready',
                  domains: data.items,
                  nextCursor: data.nextCursor,
                }
              : { kind: 'error' },
          ),
        () => live && setState({ kind: 'error' }),
      );
    return () => {
      live = false;
    };
  }, [api, site.name, attempt]);

  const update = (change: (domains: Domain[]) => Domain[]) => {
    generation.current += 1;
    setMore('idle');
    setState((current) =>
      current.kind === 'ready'
        ? { ...current, domains: change(current.domains) }
        : current,
    );
  };

  const replace = (domain: Domain) =>
    update((domains) =>
      domains.map((d) => (d.hostname === domain.hostname ? domain : d)),
    );

  const loadMore = async () => {
    if (state.kind !== 'ready' || !state.nextCursor) return;
    const started = generation.current;
    setMore('loading');
    const { data } = await api
      .GET('/api/v0/sites/{site}/domains', {
        params: {
          path: { site: site.name },
          query: { cursor: state.nextCursor },
        },
      })
      .catch(() => ({ data: undefined }));
    if (started !== generation.current) return;
    if (!data) return setMore('error');
    setMore('idle');
    setState((current) =>
      current.kind === 'ready'
        ? {
            kind: 'ready',
            domains: [
              ...current.domains,
              ...data.items.filter(
                (d) => !current.domains.some((c) => c.id === d.id),
              ),
            ],
            nextCursor: data.nextCursor,
          }
        : current,
    );
  };

  let body;
  if (state.kind === 'loading')
    body = <p role="status">Loading custom domains…</p>;
  else if (state.kind === 'error')
    body = (
      <div className="sites-error">
        <Notice tone="danger">
          Could not load the custom domains. Check your connection, then try
          again.
        </Notice>
        <Button
          type="button"
          onClick={() => {
            setState({ kind: 'loading' });
            setAttempt((n) => n + 1);
          }}
        >
          Try again
        </Button>
      </div>
    );
  else if (state.domains.length === 0)
    body = (
      <EmptyState title="No custom domains yet">
        <p>
          Add a hostname you control, such as <code>docs.example.com</code>,
          then create one DNS record for it. The site keeps its address at{' '}
          <code>{site.hostname}</code>.
        </p>
      </EmptyState>
    );
  else
    body = (
      <ul className="domain-list" aria-label="Custom domains">
        {state.domains.map((domain) => (
          <DomainRow
            key={domain.id}
            api={api}
            site={site}
            domain={domain}
            onChecked={replace}
            onGone={() =>
              update((domains) => domains.filter((d) => d.id !== domain.id))
            }
            onRemove={() => {
              setDone(null);
              setRemoving(domain);
            }}
          />
        ))}
      </ul>
    );

  return (
    <Card title="Custom domains">
      <p>
        Serve this site at an address you own too, with the same access
        settings. Visitors sign in separately on each address.
      </p>
      <AddDomainForm
        api={api}
        site={site}
        onAdded={(domain) => {
          update((domains) => [...domains, domain].sort(byHostname));
          setDone(
            domain.status === 'active'
              ? `Added ${domain.hostname}. Its DNS record already checks out, so it is live.`
              : `Added ${domain.hostname}. Create its DNS record below; Wardlock checks it automatically.`,
          );
        }}
      />
      {done && <Notice>{done}</Notice>}
      {body}
      {more === 'error' && (
        <Notice tone="danger">Could not load more domains. Try again.</Notice>
      )}
      {state.kind === 'ready' && state.nextCursor ? (
        <div>
          <Button
            type="button"
            disabled={more === 'loading'}
            onClick={loadMore}
          >
            {more === 'loading' ? 'Loading…' : 'Show more domains'}
          </Button>
        </div>
      ) : null}

      {removing && (
        <RemoveDomainDialog
          api={api}
          site={site}
          domain={removing}
          onClose={() => setRemoving(null)}
          onRemoved={() => {
            update((domains) => domains.filter((d) => d.id !== removing.id));
            setDone(
              `Removed ${removing.hostname}. Remember to delete its DNS record.`,
            );
            setRemoving(null);
          }}
        />
      )}
    </Card>
  );
}

/** The hostname field; refusals from the server land on the field. */
function AddDomainForm({
  api,
  site,
  onAdded,
}: {
  api: ApiClient;
  site: Site;
  onAdded: (domain: Domain) => void;
}) {
  const [hostname, setHostname] = useState('');
  const [busy, setBusy] = useState(false);
  const [fieldError, setFieldError] = useState<string | null>(null);
  const [formError, setFormError] = useState<string | null>(null);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setFormError(null);
    if (!hostname.trim())
      return setFieldError('Enter a hostname, such as docs.example.com.');
    setFieldError(null);
    setBusy(true);
    const result = await api
      .POST('/api/v0/sites/{site}/domains', {
        params: { path: { site: site.name } },
        body: { hostname: hostname.trim() },
      })
      .catch(() => null);
    setBusy(false);
    if (result?.data) {
      setHostname('');
      return onAdded(result.data);
    }
    const error = result?.error;
    if (error?.code === 'domain_taken')
      return setFieldError(
        'This hostname is already attached to a site on this server. If it is one of yours, remove it from that site first.',
      );
    if (error?.code === 'invalid_hostname' || error?.code === 'invalid_request')
      return setFieldError(error.message);
    setFormError(
      error?.message
        ? `Could not add the domain: ${error.message}`
        : 'Could not add the domain. Check your connection, then try again.',
    );
  };

  return (
    <form className="domain-add" onSubmit={submit} noValidate>
      {formError && <Notice tone="danger">{formError}</Notice>}
      <div className="domain-add-row">
        <TextField
          label="Hostname"
          hint="A name under a domain you control. Apex domains such as example.com work too, with the TXT record."
          name="hostname"
          autoComplete="off"
          autoCapitalize="none"
          spellCheck={false}
          inputMode="url"
          maxLength={253}
          value={hostname}
          error={fieldError ?? undefined}
          onChange={(event) => setHostname(event.currentTarget.value)}
        />
        <Button type="submit" variant="primary" disabled={busy}>
          {busy ? 'Adding…' : 'Add domain'}
        </Button>
      </div>
    </form>
  );
}

/** One record as copyable fields, exactly as the server gave them. */
function RecordFields({ record }: { record: DnsRecord }) {
  return (
    <div
      role="group"
      aria-label={`${record.type} record`}
      className="domain-record"
    >
      <p className="domain-record-type">
        Type <code>{record.type}</code>
      </p>
      <CopyField label="Name" value={record.name} />
      <CopyField
        label={record.type === 'CNAME' ? 'Target' : 'Value'}
        value={record.value}
      />
    </div>
  );
}

const dateFormat = new Intl.DateTimeFormat('en-NZ', { dateStyle: 'medium' });

function DomainRow({
  api,
  site,
  domain,
  onChecked,
  onGone,
  onRemove,
}: {
  api: ApiClient;
  site: Site;
  domain: Domain;
  onChecked: (domain: Domain) => void;
  onGone: () => void;
  onRemove: () => void;
}) {
  const [checking, setChecking] = useState(false);
  const [result, setResult] = useState<{
    tone: NoticeTone;
    text: string;
  } | null>(null);
  const failure = domain.failureReason
    ? failureText(domain.failureReason)
    : null;

  const checkNow = async () => {
    setChecking(true);
    setResult(null);
    const answer = await api
      .POST('/api/v0/sites/{site}/domains/{hostname}/verify', {
        params: { path: { site: site.name, hostname: domain.hostname } },
      })
      .catch(() => null);
    setChecking(false);
    if (answer?.data) {
      onChecked(answer.data);
      return setResult(
        answer.data.status === 'active'
          ? { tone: 'info', text: 'Checked: the record checks out.' }
          : {
              tone: 'warning',
              text: 'Checked: the record does not check out yet.',
            },
      );
    }
    const status = answer?.response.status;
    if (status === 429) {
      const wait = Number(answer?.response.headers.get('Retry-After'));
      return setResult({
        tone: 'warning',
        text:
          wait > 0
            ? `Checked moments ago. Try again in ${wait} ${wait === 1 ? 'second' : 'seconds'}.`
            : 'Checked moments ago. Try again shortly.',
      });
    }
    if (status === 404) return onGone();
    setResult({
      tone: 'danger',
      text: 'Could not check the domain. Check your connection, then try again.',
    });
  };

  const records = (
    <>
      <RecordFields record={domain.routingRecord} />
      <details className="domain-more">
        <summary>Apex domain or CDN proxy? Use a TXT record</summary>
        <div className="domain-more-body">
          <p>
            Add this TXT record when the CNAME cannot be seen: on an apex domain
            (such as <code>example.com</code>), which cannot have a CNAME and
            points at the server with A, AAAA or ALIAS records instead, or
            behind a CDN proxy (such as Cloudflare’s proxied records) that hides
            the CNAME.
          </p>
          <RecordFields record={domain.verificationRecord} />
        </div>
      </details>
    </>
  );

  return (
    <li className="domain-row" aria-label={domain.hostname}>
      <div className="domain-row-head">
        <p className="domain-row-title">
          <DomainStatus domain={domain} />
          <code className="domain-host">{domain.hostname}</code>
        </p>
        <div className="domain-row-actions">
          <Button
            type="button"
            disabled={checking}
            aria-label={`Check ${domain.hostname} now`}
            onClick={checkNow}
          >
            {checking ? 'Checking…' : 'Check now'}
          </Button>
          <Button
            type="button"
            aria-label={`Remove ${domain.hostname}`}
            onClick={onRemove}
          >
            Remove
          </Button>
        </div>
      </div>
      <p>{statusSummary(domain)}</p>
      <p className="domain-row-meta">
        <CheckTimes domain={domain} />
        {domain.status === 'pending' &&
          ` · Removed if still pending on ${dateFormat.format(
            domain.createdAt + pendingLifetime,
          )}`}
      </p>
      {result && <Notice tone={result.tone}>{result.text}</Notice>}
      {failure && (
        <div className="domain-failure">
          <p className="domain-failure-headline">{failure.headline}</p>
          {domain.failureDetail && <p>{domain.failureDetail}</p>}
          <p>{failure.next}</p>
        </div>
      )}
      {domain.status === 'active' ? (
        <details className="domain-more">
          <summary>DNS records</summary>
          <div className="domain-more-body">
            <p>Keep this record in place; Wardlock re-checks it every day.</p>
            {records}
          </div>
        </details>
      ) : (
        <div className="domain-records">
          <p>
            Create this record at your DNS provider. Some providers want only
            the part of the name before your domain.
          </p>
          {records}
        </div>
      )}
    </li>
  );
}
