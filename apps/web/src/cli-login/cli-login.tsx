// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { components } from '@wardlock/apiclient-ts';
import { Button, Notice, PanelLayout, TextField } from '@wardlock/ui';
import {
  useEffect,
  useId,
  useRef,
  useState,
  type FormEvent,
  type RefObject,
} from 'react';
import { failure, unreachable } from '../auth/messages.js';
import { useSession } from '../auth/session.js';
import { useTitle } from '../shell/page.js';
import { navigate, useLocation } from '../shell/router.js';
import './cli-login.css';

type Scope = components['schemas']['Scope'];

/** Where `wardlock login` sends the owner (docs/SPEC.md, "CLI login"). */
export const cliLoginPath = '/cli-login';

const codeLetters = 'BCDFGHJKLMNPQRSTVWXZ';

/**
 * Accepts a code as typed (any case, with or without the hyphen or spaces)
 * and returns it as the CLI shows it, XXXX-XXXX, or null.
 */
export function normaliseUserCode(raw: string) {
  const letters = raw.toUpperCase().replace(/[\s-]/g, '');
  if (
    letters.length !== 8 ||
    [...letters].some((c) => !codeLetters.includes(c))
  )
    return null;
  return `${letters.slice(0, 4)}-${letters.slice(4)}`;
}

const scopeChoices: { scope: Scope; label: string; description: string }[] = [
  {
    scope: 'deploy',
    label: 'Deploy',
    description: 'Create sites, upload deploys and roll back.',
  },
  {
    scope: 'manage',
    label: 'Manage',
    description:
      'Delete sites, and change custom domains and who can open a site.',
  },
];

type Step =
  | { kind: 'enter'; invalid: boolean }
  | { kind: 'review'; code: string }
  | { kind: 'done'; outcome: 'approved' | 'denied' };

/**
 * `/cli-login`: the signed-in owner confirms the code `wardlock login` shows
 * and approves or denies it. `?code=` comes from the address the CLI opens;
 * without it the owner types the code.
 */
export function CliLoginPage() {
  const { params } = useLocation();
  const fromUrl = normaliseUserCode(params.get('code') ?? '');
  const [step, setStep] = useState<Step>(
    fromUrl
      ? { kind: 'review', code: fromUrl }
      : { kind: 'enter', invalid: false },
  );
  const heading = useRef<HTMLHeadingElement>(null);
  const firstRender = useRef(true);
  useEffect(() => {
    if (firstRender.current) firstRender.current = false;
    else heading.current?.focus();
  }, [step.kind]);

  if (step.kind === 'enter')
    return (
      <EnterCode
        heading={heading}
        invalid={step.invalid}
        onCode={(code) => {
          navigate(`${cliLoginPath}?code=${code}`, { replace: true });
          setStep({ kind: 'review', code });
        }}
      />
    );
  if (step.kind === 'review')
    return (
      <Review
        heading={heading}
        code={step.code}
        onDone={(outcome) => setStep({ kind: 'done', outcome })}
        onInvalid={() => {
          navigate(cliLoginPath, { replace: true });
          setStep({ kind: 'enter', invalid: true });
        }}
      />
    );
  return <Done heading={heading} outcome={step.outcome} />;
}

type HeadingRef = RefObject<HTMLHeadingElement | null>;

function EnterCode({
  heading,
  invalid,
  onCode,
}: {
  heading: HeadingRef;
  invalid: boolean;
  onCode: (code: string) => void;
}) {
  useTitle('Log in to the CLI');
  const [value, setValue] = useState('');
  const [error, setError] = useState<string | null>(null);
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const code = normaliseUserCode(value);
    if (!code) {
      setError('Enter the 8-letter code from your terminal, like BDFH-KMPR.');
      return;
    }
    onCode(code);
  };
  return (
    <PanelLayout productName="Wardlock">
      <div className="auth-heading">
        <h1 className="auth-title" ref={heading} tabIndex={-1}>
          Log in to the Wardlock CLI
        </h1>
        <p>
          Run <code className="cli-command">wardlock login</code> in your
          terminal and enter the code it shows.
        </p>
      </div>
      {invalid ? (
        <Notice tone="danger">
          That code is wrong, has expired or was already used. Codes last 15
          minutes; run <code className="cli-command">wardlock login</code> again
          for a new one.
        </Notice>
      ) : null}
      <form className="auth-form" onSubmit={submit} noValidate>
        <TextField
          label="Code from your terminal"
          name="code"
          className="auth-code"
          autoComplete="off"
          autoCapitalize="characters"
          spellCheck={false}
          maxLength={11}
          value={value}
          onChange={(event) => setValue(event.target.value)}
          error={error}
        />
        <Button type="submit" variant="primary">
          Continue
        </Button>
      </form>
    </PanelLayout>
  );
}

function Review({
  heading,
  code,
  onDone,
  onInvalid,
}: {
  heading: HeadingRef;
  code: string;
  onDone: (outcome: 'approved' | 'denied') => void;
  onInvalid: () => void;
}) {
  useTitle('Approve CLI login');
  const { api, session } = useSession();
  // Least privilege: Deploy only, unless the owner also ticks Manage.
  const [scopes, setScopes] = useState<Set<Scope>>(
    () => new Set<Scope>(['deploy']),
  );
  const [scopeError, setScopeError] = useState<string | null>(null);
  const [formError, setFormError] = useState<string | null>(null);
  const [busy, setBusy] = useState<'approve' | 'deny' | null>(null);
  const scopesId = useId();
  const email =
    session.status === 'signed-in' ? session.me.owner.email : undefined;

  const toggle = (scope: Scope, on: boolean) => {
    setScopeError(null);
    setScopes((current) => {
      const next = new Set(current);
      if (on) next.add(scope);
      else next.delete(scope);
      return next;
    });
  };

  const decide = async (decision: 'approve' | 'deny') => {
    const chosen = scopeChoices
      .map((c) => c.scope)
      .filter((s) => scopes.has(s));
    if (decision === 'approve' && chosen.length === 0) {
      setScopeError('Choose at least one thing this computer may do.');
      return;
    }
    setBusy(decision);
    setFormError(null);
    try {
      const { error, response } =
        decision === 'approve'
          ? await api.POST('/api/v0/cli-login/approve', {
              body: { userCode: code, scopes: chosen },
            })
          : await api.POST('/api/v0/cli-login/deny', {
              body: { userCode: code },
            });
      if (response.ok) {
        onDone(decision === 'approve' ? 'approved' : 'denied');
        return;
      }
      if (error?.code === 'invalid_code') {
        onInvalid();
        return;
      }
      setFormError(failure(response));
    } catch {
      setFormError(unreachable);
    }
    setBusy(null);
  };

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    void decide('approve');
  };

  return (
    <PanelLayout productName="Wardlock">
      <div className="auth-heading">
        <h1 className="auth-title" ref={heading} tabIndex={-1}>
          Approve CLI login
        </h1>
        <p>
          A <code className="cli-command">wardlock login</code> is waiting for
          approval
          {email ? (
            <>
              {' '}
              to act as <strong className="auth-email">{email}</strong>
            </>
          ) : null}
          .
        </p>
      </div>
      <div className="cli-code-box">
        <p className="cli-code-label">
          Check this matches the code in your terminal
        </p>
        <p className="cli-code">{code}</p>
      </div>
      <Notice tone="warning">
        Only approve a login you started yourself. Anyone who asked you to
        approve a code could use your sites with it.
      </Notice>
      {formError ? <Notice tone="danger">{formError}</Notice> : null}
      <form className="auth-form" onSubmit={submit} noValidate>
        <fieldset
          className="cli-scopes"
          aria-describedby={scopeError ? `${scopesId}-error` : undefined}
        >
          <legend className="cli-scopes-legend">
            What may this computer do?
          </legend>
          {scopeChoices.map((choice) => (
            <label className="cli-scope" key={choice.scope}>
              <input
                type="checkbox"
                className="cli-scope-input"
                name="scopes"
                value={choice.scope}
                checked={scopes.has(choice.scope)}
                onChange={(event) => toggle(choice.scope, event.target.checked)}
              />
              <span className="cli-scope-text">
                <span className="cli-scope-label">{choice.label}</span>
                <span className="cli-scope-description">
                  {choice.description}
                </span>
              </span>
            </label>
          ))}
          {scopeError ? (
            <p
              className="cli-scopes-error"
              id={`${scopesId}-error`}
              role="alert"
            >
              {scopeError}
            </p>
          ) : null}
        </fieldset>
        <p className="cli-note">
          It gets an API token you can revoke at any time. Both kinds of access
          can read your sites and deploys.
        </p>
        <div className="auth-actions">
          <Button type="submit" variant="primary" disabled={busy !== null}>
            {busy === 'approve' ? 'Approving…' : 'Approve'}
          </Button>
          <Button
            type="button"
            disabled={busy !== null}
            onClick={() => void decide('deny')}
          >
            {busy === 'deny' ? 'Denying…' : 'Deny'}
          </Button>
        </div>
      </form>
    </PanelLayout>
  );
}

function Done({
  heading,
  outcome,
}: {
  heading: HeadingRef;
  outcome: 'approved' | 'denied';
}) {
  useTitle(outcome === 'approved' ? 'CLI login approved' : 'CLI login denied');
  return (
    <PanelLayout productName="Wardlock">
      <div className="auth-heading">
        <h1 className="auth-title" ref={heading} tabIndex={-1}>
          {outcome === 'approved' ? 'CLI login approved' : 'CLI login denied'}
        </h1>
        <p>
          {outcome === 'approved'
            ? 'Return to your terminal: wardlock finishes logging in within a few seconds. You can close this page.'
            : 'The waiting wardlock login was refused and got no access. If you did not start it, nothing more is needed.'}
        </p>
      </div>
      <a className="cli-home" href="/">
        Go to the dashboard
      </a>
    </PanelLayout>
  );
}
