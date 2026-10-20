// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import { Button, Notice, PanelLayout, TextField } from '@wardlock/ui';
import { useEffect, useState, type FormEvent } from 'react';
import { Redirect, useTitle } from '../shell/page.js';
import { navigate, useLocation } from '../shell/router.js';
import { PolicyAgreement } from '../policy/acceptable-use.js';
import { failure, unreachable } from './messages.js';
import { useSession } from './session.js';

/**
 * `/setup?code=…`: the one-time setup URL `wardlockd serve` prints while no
 * owner exists. Creates the first owner and signs them in.
 */
export function SetupPage() {
  const { api, session, signedIn } = useSession();
  const { params } = useLocation();
  // Read once, then taken out of the address bar and history.
  const [code] = useState(() => params.get('code'));
  const [email, setEmail] = useState('');
  const [fieldError, setFieldError] = useState<string | null>(null);
  const [formError, setFormError] = useState<string | null>(null);
  const [expired, setExpired] = useState(false);
  const [busy, setBusy] = useState(false);
  useTitle('Set up Wardlock');

  useEffect(() => {
    // Unless a redirect has already left the page.
    if (code && window.location.pathname === '/setup')
      navigate('/setup', { replace: true });
  }, [code]);

  if (session.status === 'signed-in' && !busy) return <Redirect to="/" />;

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const address = email.trim();
    if (!address) {
      setFieldError('Enter your email address.');
      return;
    }
    if (!code) return;
    setBusy(true);
    setFieldError(null);
    setFormError(null);
    try {
      const { data, error, response } = await api.POST('/api/v0/setup', {
        body: { code, email: address },
      });
      if (data) {
        signedIn(data);
        navigate('/', { replace: true });
        return;
      }
      if (error?.code === 'invalid_code') setExpired(true);
      else if (error?.code === 'invalid_request')
        setFieldError('Enter an email address, like name@example.com.');
      else setFormError(failure(response));
    } catch {
      setFormError(unreachable);
    }
    setBusy(false);
  };

  if (!code || expired)
    return (
      <PanelLayout productName="Wardlock">
        <div className="auth-heading">
          <h1 className="auth-title">
            {code ? 'This setup link no longer works' : 'Set up Wardlock'}
          </h1>
          <p>
            {code
              ? 'Setup links work once, expire after 60 minutes and stop working once an owner exists.'
              : 'Open the setup URL that wardlockd printed in its log. It works once and expires after 60 minutes.'}
          </p>
        </div>
        <Notice tone="info">
          Restart wardlockd to print a new setup URL, or create the owner with{' '}
          <code>wardlockd admin create --email you@example.com</code>. If an
          owner already exists, <a href="/sign-in">sign in</a> instead.
        </Notice>
      </PanelLayout>
    );

  return (
    <PanelLayout productName="Wardlock">
      <div className="auth-heading">
        <h1 className="auth-title">Create the first owner</h1>
        <p>
          You will sign in to this dashboard with this email address. Wardlock
          sends sign-in codes to it, so use one you can receive mail at.
        </p>
      </div>
      {formError ? <Notice tone="danger">{formError}</Notice> : null}
      <form className="auth-form" onSubmit={submit} noValidate>
        <TextField
          label="Email address"
          type="email"
          name="email"
          autoComplete="username email"
          inputMode="email"
          spellCheck={false}
          value={email}
          onChange={(event) => setEmail(event.target.value)}
          error={fieldError}
        />
        <Button type="submit" variant="primary" disabled={busy}>
          {busy ? 'Creating owner…' : 'Create owner and sign in'}
        </Button>
      </form>
      <PolicyAgreement />
    </PanelLayout>
  );
}
