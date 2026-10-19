// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient } from '@wardlock/apiclient-ts';
import { Button, Card, Notice, TextField } from '@wardlock/ui';
import { useEffect, useState } from 'react';
import { PolicyBadge, type Policy, type Site } from '../sites/format.js';
import { DeletePolicyDialog } from './delete.js';
import { PolicyDialog } from './edit.js';
import { allowlistText, byPath, governing, methodsText } from './format.js';
import './policies.css';

type ListState =
  | { kind: 'loading' }
  | { kind: 'error' }
  | { kind: 'ready'; policies: Policy[] };

type Open =
  | { kind: 'none' }
  | { kind: 'add' }
  | { kind: 'edit' | 'delete'; policy: Policy };

/** Every policy of a site, following each page of the listing. */
async function loadPolicies(api: ApiClient, site: string) {
  const policies: Policy[] = [];
  let cursor: string | undefined;
  do {
    const { data } = await api.GET('/api/v0/sites/{site}/policies', {
      params: { path: { site }, query: cursor ? { cursor } : {} },
    });
    if (!data) throw new Error('could not load the policies');
    policies.push(...data.items);
    cursor = data.nextCursor;
  } while (cursor);
  return policies;
}

/**
 * A site's access policies: the default and any path policies, with how
 * the longest matching path decides each request, a path checker, and the
 * editor. `onChange` runs after every saved change so the rest of the page
 * can show the new access.
 */
export function PoliciesCard({
  api,
  site,
  onChange,
}: {
  api: ApiClient;
  site: Site;
  onChange?: () => void;
}) {
  const [state, setState] = useState<ListState>({ kind: 'loading' });
  const [open, setOpen] = useState<Open>({ kind: 'none' });
  const [done, setDone] = useState<string | null>(null);
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    let live = true;
    loadPolicies(api, site.name).then(
      (policies) => live && setState({ kind: 'ready', policies }),
      () => live && setState({ kind: 'error' }),
    );
    return () => {
      live = false;
    };
  }, [api, site.name, attempt]);

  const close = () => setOpen({ kind: 'none' });

  if (state.kind === 'loading')
    return (
      <Card title="Access policies">
        <p role="status">Loading access policies…</p>
      </Card>
    );
  if (state.kind === 'error')
    return (
      <Card title="Access policies">
        <div className="sites-error">
          <Notice tone="danger">
            Could not load the access policies. Check your connection, then try
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
      </Card>
    );

  // Listing stores the site default first if it was never set, so it is
  // always here.
  const { policies } = state;

  const saved = (policy: Policy, before: Policy | undefined) => {
    setState({
      kind: 'ready',
      policies: [
        ...policies.filter((p) => p.path !== policy.path),
        policy,
      ].sort(byPath),
    });
    const ended =
      before && policy.version > before.version
        ? ' Visitors who got in under the old settings need to get in again.'
        : '';
    setDone(
      `Saved ${policy.path === '/' ? 'the site default' : `the policy for ${policy.path}`}.${ended}`,
    );
    close();
    onChange?.();
  };

  const deleted = (policy: Policy) => {
    setState({
      kind: 'ready',
      policies: policies.filter((p) => p.id !== policy.id),
    });
    setDone(`Deleted the policy for ${policy.path}.`);
    close();
    onChange?.();
  };

  return (
    <Card title="Access policies">
      <div className="policy-intro">
        <p>
          Each request follows the one policy with the longest path that matches
          it, or the site default when none does. Policies do not combine: a
          public <code>/assets/</code> stays public under a protected site.
        </p>
        <Button
          type="button"
          onClick={() => {
            setDone(null);
            setOpen({ kind: 'add' });
          }}
        >
          Add path policy
        </Button>
      </div>
      {done && <Notice>{done}</Notice>}
      <ul className="policy-list" aria-label="Access policies">
        {policies.map((policy) => (
          <PolicyRow
            key={policy.id}
            policy={policy}
            onEdit={() => {
              setDone(null);
              setOpen({ kind: 'edit', policy });
            }}
            onDelete={
              policy.path === '/'
                ? undefined
                : () => {
                    setDone(null);
                    setOpen({ kind: 'delete', policy });
                  }
            }
          />
        ))}
      </ul>
      <PathCheck policies={policies} />

      {(open.kind === 'add' || open.kind === 'edit') && (
        <PolicyDialog
          api={api}
          site={site}
          policy={open.kind === 'edit' ? open.policy : undefined}
          policies={policies}
          onClose={close}
          onSaved={(policy) =>
            saved(policy, open.kind === 'edit' ? open.policy : undefined)
          }
        />
      )}
      {open.kind === 'delete' && (
        <DeletePolicyDialog
          api={api}
          site={site}
          policy={open.policy}
          policies={policies}
          onClose={close}
          onDeleted={() => deleted(open.policy)}
        />
      )}
    </Card>
  );
}

/** One policy: its path, access, ways in, allowlist and actions. */
function PolicyRow({
  policy,
  onEdit,
  onDelete,
}: {
  policy: Policy;
  onEdit: () => void;
  /** Absent for the site default, which cannot be deleted. */
  onDelete: (() => void) | undefined;
}) {
  const isDefault = policy.path === '/';
  const name = isDefault ? 'the site default' : `the policy for ${policy.path}`;
  const allowlist =
    policy.visibility === 'protected' &&
    (policy.methods.includes('email') || policy.methods.includes('oauth'))
      ? allowlistText(policy)
      : '';
  return (
    <li className="policy-row">
      <div className="policy-row-main">
        <p className="policy-row-title">
          <code>{policy.path}</code>
          {isDefault && <span>Site default</span>}
          <PolicyBadge policy={policy} />
        </p>
        <p className="policy-row-meta">
          {isDefault
            ? 'Covers every path without a policy of its own.'
            : `Covers ${policy.path} and everything under it.`}{' '}
          {policy.visibility === 'public'
            ? 'Anyone with the address can open it.'
            : `Ways in: ${methodsText(policy)}.`}
        </p>
        {allowlist && (
          <p className="policy-row-meta">Allowlist: {allowlist}.</p>
        )}
      </div>
      <div className="policy-row-actions">
        <Button type="button" aria-label={`Edit ${name}`} onClick={onEdit}>
          Edit
        </Button>
        {onDelete && (
          <Button
            type="button"
            aria-label={`Delete ${name}`}
            onClick={onDelete}
          >
            Delete
          </Button>
        )}
      </div>
    </li>
  );
}

/** Answers "which policy decides this path?" as the owner types. */
function PathCheck({ policies }: { policies: Policy[] }) {
  const [path, setPath] = useState('');
  const typed = path.trim().replace(/[?#].*$/, '');
  const target = typed && !typed.startsWith('/') ? `/${typed}` : typed;
  const policy = target ? governing(policies, target) : undefined;
  return (
    <div className="policy-check">
      <TextField
        label="Check a path"
        hint="Type a path on the site, such as /docs/guide.html, to see which policy decides it."
        autoComplete="off"
        autoCapitalize="none"
        spellCheck={false}
        className="policy-mono"
        value={path}
        onChange={(event) => setPath(event.target.value)}
      />
      <p className="policy-check-result" role="status">
        {policy && (
          <>
            <code>{target}</code> follows{' '}
            {policy.path === '/' ? (
              'the site default'
            ) : (
              <>
                the policy for <code>{policy.path}</code>
              </>
            )}
            :{' '}
            {policy.visibility === 'public'
              ? 'public'
              : `protected (${methodsText(policy)})`}
            .
          </>
        )}
      </p>
    </div>
  );
}
