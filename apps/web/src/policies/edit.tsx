// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient } from '@wardlock/apiclient-ts';
import {
  Button,
  Checkbox,
  Dialog,
  Fieldset,
  Notice,
  Radio,
  TextAreaField,
  TextField,
} from '@wardlock/ui';
import { useId, useState, type FormEvent } from 'react';
import type { Policy, Site } from '../sites/format.js';
import {
  bytes,
  characters,
  methods,
  parseEntries,
  parseOrgs,
  policyName,
  type PolicyMethod,
  type SetPolicyRequest,
} from './format.js';

const fields = [
  'path',
  'visibility',
  'methods',
  'password',
  'allowlist',
] as const;
type Field = (typeof fields)[number];
type Errors = Partial<Record<Field, string>>;

const minPassword = 8;
const maxPasswordBytes = 1024;

const methodText: Record<PolicyMethod, { label: string; hint: string }> = {
  links: {
    label: 'Share links',
    hint: 'Anyone with a share link for this path gets straight in, with no login page.',
  },
  password: {
    label: 'Password',
    hint: 'One password for everyone you give it to, entered on the login page.',
  },
  email: {
    label: 'Email code',
    hint: 'Visitors enter an address on the allowlist and get a 6-digit code by email.',
  },
  oauth: {
    label: 'Sign in with GitHub, Google or single sign-on',
    hint: 'Visitors sign in with an account the allowlist lets in, through the providers set up on this server.',
  },
};

/**
 * Edits one policy, or adds a path policy when `policy` is absent. The
 * password is write-only: the field starts empty, a blank field keeps the
 * current password, and the typed text is dropped when the dialog closes.
 */
export function PolicyDialog({
  api,
  site,
  policy,
  policies,
  onClose,
  onSaved,
}: {
  api: ApiClient;
  site: Site;
  /** The policy to edit; absent to add a path policy. */
  policy: Policy | undefined;
  /** Every policy of the site, to refuse a path that already has one. */
  policies: Policy[];
  onClose: () => void;
  onSaved: (saved: Policy) => void;
}) {
  const formId = useId();
  const adding = !policy;
  const [path, setPath] = useState(policy?.path ?? '');
  const [visibility, setVisibility] = useState(
    policy?.visibility ?? 'protected',
  );
  const [chosen, setChosen] = useState<PolicyMethod[]>(
    policy && policy.methods.length > 0 ? policy.methods : ['links'],
  );
  const [password, setPassword] = useState('');
  const [entries, setEntries] = useState(() =>
    policy
      ? [...policy.allowlist.emails, ...policy.allowlist.domains].join('\n')
      : '',
  );
  const [orgs, setOrgs] = useState(() =>
    (policy?.allowlist.githubOrgs ?? []).join('\n'),
  );
  const [errors, setErrors] = useState<Errors>({});
  const [failure, setFailure] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // A password the server already holds; only it can be kept.
  const hasPassword = !!policy?.methods.includes('password');
  const protect = visibility === 'protected';
  const on = (method: PolicyMethod) => chosen.includes(method);
  const usesAllowlist = on('email') || on('oauth');
  const allowlistCount = policy
    ? policy.allowlist.emails.length +
      policy.allowlist.domains.length +
      (policy.allowlist.githubOrgs?.length ?? 0)
    : 0;

  const toggle = (method: PolicyMethod, checked: boolean) =>
    setChosen((current) =>
      checked
        ? methods.filter((m) => m === method || current.includes(m))
        : current.filter((m) => m !== method),
    );

  const close = () => {
    if (!busy) onClose();
  };

  /** The request, or the field errors that stop it. */
  const build = (): { body: SetPolicyRequest } | { errors: Errors } => {
    const found: Errors = {};
    const target = adding ? path.trim() : policy.path;
    if (adding) {
      if (!target.startsWith('/') || !target.endsWith('/') || target === '/')
        found.path =
          'Enter a folder that starts and ends with a slash, such as /docs/.';
      else if (policies.some((p) => p.path === target))
        found.path = `${target} already has a policy. Close this and edit that one instead.`;
    }
    if (!protect) {
      if (Object.keys(found).length > 0) return { errors: found };
      return { body: { path: target, visibility: 'public', methods: [] } };
    }
    const body: SetPolicyRequest = {
      path: target,
      visibility: 'protected',
      methods: chosen,
    };
    if (chosen.length === 0) found.methods = 'Choose at least one way in.';
    if (on('password')) {
      if (password === '' && !hasPassword)
        found.password = `Enter a password of at least ${minPassword} characters.`;
      else if (password !== '' && characters(password) < minPassword)
        found.password = `Use at least ${minPassword} characters.`;
      else if (bytes(password) > maxPasswordBytes)
        found.password = 'That password is too long. Use a shorter one.';
      else if (password !== '') body.password = password;
    }
    if (usesAllowlist) {
      const { emails, domains } = parseEntries(entries);
      const githubOrgs = parseOrgs(orgs);
      if (on('email') && emails.length + domains.length === 0)
        found.allowlist = 'Email codes need at least one address or domain.';
      else if (
        on('oauth') &&
        emails.length + domains.length + githubOrgs.length === 0
      )
        found.allowlist =
          'Sign-in needs at least one address, domain or GitHub organisation.';
      // Organisations are only shown, and so only sent, with sign-in on;
      // leaving them out keeps the current ones.
      body.allowlist = on('oauth')
        ? { emails, domains, githubOrgs }
        : { emails, domains };
    }
    return Object.keys(found).length > 0 ? { errors: found } : { body };
  };

  const save = async (event: FormEvent) => {
    event.preventDefault();
    const built = build();
    setFailure(null);
    if ('errors' in built) return setErrors(built.errors);
    setErrors({});
    setBusy(true);
    const result = await api
      .PUT('/api/v0/sites/{site}/policies', {
        params: { path: { site: site.name } },
        body: built.body,
      })
      .catch(() => null);
    setBusy(false);
    if (result?.data) {
      setPassword('');
      return onSaved(result.data);
    }
    const error = result?.error;
    const target = fields.find((field) => field === error?.target);
    if (
      (error?.code === 'invalid_policy' ||
        error?.code === 'invalid_policy_path') &&
      target &&
      (target !== 'path' || adding)
    )
      return setErrors({ [target]: error.message });
    setFailure(
      error?.message
        ? `Could not save the policy: ${error.message}`
        : 'Could not save the policy. Check your connection, then try again.',
    );
  };

  const title = adding
    ? `Add a path policy to ${site.name}`
    : policy.path === '/'
      ? `Edit the site default for ${site.name}`
      : `Edit the policy for ${policy.path}`;

  return (
    <Dialog
      open
      title={title}
      onClose={close}
      actions={
        <>
          <Button type="button" disabled={busy} onClick={close}>
            Cancel
          </Button>
          <Button type="submit" form={formId} variant="primary" disabled={busy}>
            {busy ? 'Saving…' : adding ? 'Add policy' : 'Save policy'}
          </Button>
        </>
      }
    >
      <p>
        {adding
          ? 'A path policy decides who can open one folder, and everything under it, instead of the site default.'
          : policy.path === '/'
            ? 'The site default decides who can open every path that has no policy of its own.'
            : `This policy decides who can open ${policy.path} and everything under it, instead of the site default.`}{' '}
        Saving a change signs out visitors who got in under the old settings.
      </p>
      <form
        id={formId}
        className="policy-form"
        onSubmit={save}
        noValidate
        autoComplete="off"
      >
        {adding && (
          <TextField
            label="Folder"
            hint="Starts and ends with a slash, such as /docs/ or /internal/."
            autoComplete="off"
            autoCapitalize="none"
            spellCheck={false}
            className="policy-mono"
            value={path}
            error={errors.path}
            onChange={(event) => setPath(event.target.value)}
          />
        )}
        <Fieldset legend="Who can open it" error={errors.visibility}>
          <Radio
            name={`${formId}-visibility`}
            value="public"
            label="Public"
            hint="Anyone with the address."
            checked={!protect}
            onChange={() => setVisibility('public')}
          />
          <Radio
            name={`${formId}-visibility`}
            value="protected"
            label="Protected"
            hint="Only visitors who get in one of the ways you choose."
            checked={protect}
            onChange={() => setVisibility('protected')}
          />
        </Fieldset>
        {!protect &&
          allowlistCount > 0 &&
          policy?.visibility === 'protected' && (
            <Notice tone="warning">
              Making {policyName(policy.path)} public also clears its allowlist.
            </Notice>
          )}
        {protect && (
          <Fieldset
            legend="Ways in"
            hint="Visitors need any one of these, not all of them."
            error={errors.methods}
          >
            {methods.map((method) => (
              <div key={method} className="policy-method">
                <Checkbox
                  label={methodText[method].label}
                  hint={methodText[method].hint}
                  checked={on(method)}
                  onChange={(event) => toggle(method, event.target.checked)}
                />
                {method === 'password' && on('password') && (
                  <div className="policy-method-detail">
                    <TextField
                      label={
                        hasPassword
                          ? 'New visitor password'
                          : 'Visitor password'
                      }
                      type="password"
                      autoComplete="new-password"
                      hint={
                        hasPassword
                          ? 'A password is set. Leave this blank to keep it; it is never shown again.'
                          : `At least ${minPassword} characters. It is never shown again after you save.`
                      }
                      value={password}
                      error={errors.password}
                      onChange={(event) => setPassword(event.target.value)}
                    />
                  </div>
                )}
              </div>
            ))}
          </Fieldset>
        )}
        {protect && usesAllowlist && (
          <TextAreaField
            label="Allowlist"
            hint="Email addresses and domains, one per line. example.com lets in addresses at that domain; *.example.com lets in its subdomains. Shared by email codes and sign-in."
            autoCapitalize="none"
            spellCheck={false}
            className="policy-mono"
            rows={4}
            value={entries}
            error={errors.allowlist}
            onChange={(event) => setEntries(event.target.value)}
          />
        )}
        {protect && on('oauth') && (
          <TextAreaField
            label="GitHub organisations"
            hint="Organisation logins, one per line. Their members get in when they sign in with GitHub."
            autoCapitalize="none"
            spellCheck={false}
            className="policy-mono"
            rows={2}
            value={orgs}
            onChange={(event) => setOrgs(event.target.value)}
          />
        )}
      </form>
      {failure && <Notice tone="danger">{failure}</Notice>}
    </Dialog>
  );
}
