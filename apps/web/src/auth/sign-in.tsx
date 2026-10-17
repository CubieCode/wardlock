// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient } from '@wardlock/apiclient-ts';
import { Button, Notice, PanelLayout, TextField } from '@wardlock/ui';
import { useEffect, useRef, useState, type FormEvent } from 'react';
import { Redirect, useTitle } from '../shell/page.js';
import { navigate, useLocation, safeReturnPath } from '../shell/router.js';
import { failure, unreachable } from './messages.js';
import { useSession, type Me } from './session.js';

type LinkResult =
  | { kind: 'ok'; me: Me }
  | { kind: 'invalid' }
  | { kind: 'failed'; message: string };

// A sign-in link code works once. React may run an effect twice (StrictMode,
// remounts), so each code is redeemed at most once per page load.
const redemptions = new Map<string, Promise<LinkResult>>();

function redeem(api: ApiClient, code: string) {
  let pending = redemptions.get(code);
  if (!pending) {
    pending = api
      .POST('/api/v0/auth/sign-in-link', { body: { code } })
      .then(({ data, error, response }): LinkResult => {
        if (data) return { kind: 'ok', me: data };
        if (error?.code === 'invalid_code') return { kind: 'invalid' };
        return { kind: 'failed', message: failure(response) };
      })
      .catch((): LinkResult => ({ kind: 'failed', message: unreachable }));
    redemptions.set(code, pending);
  }
  return pending;
}

/** The sign-in page, returning to `next` afterwards. */
export const signInPath = (next: string) =>
  next === '/' ? '/sign-in' : `/sign-in?next=${encodeURIComponent(next)}`;

/**
 * `/sign-in`: email-code sign-in, and the landing page for one-time sign-in
 * links (`/sign-in?code=…` from `wardlockd admin create` and invitations).
 * `next` names a same-origin path to return to afterwards.
 */
export function SignInPage() {
  const { session } = useSession();
  const { params } = useLocation();
  // Read once: the code leaves the address bar as soon as the page loads.
  const [linkCode, setLinkCode] = useState(() => params.get('code'));
  const next = safeReturnPath(params.get('next'));

  if (linkCode)
    return (
      <SignInLink
        code={linkCode}
        next={next}
        onDismiss={() => setLinkCode(null)}
      />
    );
  if (session.status === 'signed-in') return <Redirect to={next} />;
  return <EmailCodeSignIn next={next} />;
}

function SignInLink({
  code,
  next,
  onDismiss,
}: {
  code: string;
  next: string;
  /** Leaves a failed link for email-code sign-in. */
  onDismiss: () => void;
}) {
  const { api, signedIn } = useSession();
  const [result, setResult] = useState<LinkResult | null>(null);
  useTitle(result ? 'Sign-in link' : 'Signing in');

  useEffect(() => {
    let live = true;
    // Take the code out of the address bar and history before using it,
    // keeping where to go afterwards.
    navigate(signInPath(next), { replace: true });
    void redeem(api, code).then((outcome) => {
      if (!live) return;
      if (outcome.kind === 'ok') {
        signedIn(outcome.me);
        navigate(next, { replace: true });
      } else setResult(outcome);
    });
    return () => {
      live = false;
    };
  }, [api, code, next, signedIn]);

  if (!result || result.kind === 'ok')
    return (
      <PanelLayout productName="Wardlock">
        <h1 className="auth-title">Signing in</h1>
        <p role="status">Checking your sign-in link…</p>
      </PanelLayout>
    );
  return (
    <PanelLayout productName="Wardlock" homeHref="/sign-in">
      <div className="auth-heading">
        <h1 className="auth-title">
          {result.kind === 'invalid'
            ? 'This sign-in link no longer works'
            : 'Could not sign you in'}
        </h1>
        <p>
          {result.kind === 'invalid'
            ? 'Sign-in links work once and expire. Sign in with a code sent to your email instead.'
            : result.message}
        </p>
      </div>
      <Button type="button" variant="primary" onClick={onDismiss}>
        Sign in with an email code
      </Button>
    </PanelLayout>
  );
}

type Step =
  { kind: 'email' } | { kind: 'code'; email: string; resent: boolean };

function EmailCodeSignIn({ next }: { next: string }) {
  const { api, session, signedIn } = useSession();
  const [step, setStep] = useState<Step>({ kind: 'email' });
  const [email, setEmail] = useState('');
  const [code, setCode] = useState('');
  const [fieldError, setFieldError] = useState<string | null>(null);
  const [formError, setFormError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const heading = useRef<HTMLHeadingElement>(null);
  const codeStep = step.kind === 'code';
  useTitle(codeStep ? 'Check your email' : 'Sign in');

  // Move focus to the new heading when the step changes, so screen readers
  // announce it, but not on first load.
  const firstRender = useRef(true);
  useEffect(() => {
    if (firstRender.current) firstRender.current = false;
    else heading.current?.focus();
  }, [codeStep]);

  const notice =
    session.status === 'signed-out' && session.reason === 'expired'
      ? 'Your session has ended. Sign in again to continue.'
      : session.status === 'signed-out' && session.reason === 'signed-out'
        ? 'You have signed out.'
        : null;

  const sendCode = async (address: string, resend: boolean) => {
    setBusy(true);
    setFormError(null);
    setFieldError(null);
    try {
      const { error, response } = await api.POST('/api/v0/auth/email-code', {
        body: { email: address },
      });
      if (response.ok) {
        setCode('');
        setStep({ kind: 'code', email: address, resent: resend });
      } else if (error?.code === 'invalid_request' && !resend)
        setFieldError('Enter an email address, like name@example.com.');
      else setFormError(failure(response));
    } catch {
      setFormError(unreachable);
    } finally {
      setBusy(false);
    }
  };

  const submitEmail = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const address = email.trim();
    if (!address) {
      setFieldError('Enter your email address.');
      return;
    }
    void sendCode(address, false);
  };

  const submitCode = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (step.kind !== 'code') return;
    const digits = code.replace(/\s/g, '');
    if (!/^\d{6}$/.test(digits)) {
      setFieldError('Enter the 6-digit code from the email.');
      return;
    }
    setBusy(true);
    setFormError(null);
    setFieldError(null);
    try {
      const { data, error, response } = await api.POST(
        '/api/v0/auth/email-code/verify',
        { body: { email: step.email, code: digits } },
      );
      if (data) {
        signedIn(data);
        navigate(next, { replace: true });
        return;
      }
      if (error?.code === 'invalid_code')
        setFieldError(
          'That code is not right, or it has expired. Check you used the latest email, or send a new code.',
        );
      else setFormError(failure(response));
    } catch {
      setFormError(unreachable);
    }
    setBusy(false);
  };

  if (step.kind === 'email')
    return (
      <PanelLayout productName="Wardlock" homeHref="/sign-in">
        <div className="auth-heading">
          <h1 className="auth-title" ref={heading} tabIndex={-1}>
            Sign in to Wardlock
          </h1>
          <p>We will email you a code to sign in. There is no password.</p>
        </div>
        {notice ? <Notice tone="info">{notice}</Notice> : null}
        {formError ? <Notice tone="danger">{formError}</Notice> : null}
        <form className="auth-form" onSubmit={submitEmail} noValidate>
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
            {busy ? 'Sending code…' : 'Send code'}
          </Button>
        </form>
      </PanelLayout>
    );

  return (
    <PanelLayout productName="Wardlock" homeHref="/sign-in">
      <div className="auth-heading">
        <h1 className="auth-title" ref={heading} tabIndex={-1}>
          Check your email
        </h1>
        <p>
          If <strong className="auth-email">{step.email}</strong> belongs to an
          owner of this server, we have sent it a 6‑digit code. The code expires
          in 10 minutes.
        </p>
      </div>
      {step.resent && !fieldError && !formError ? (
        <Notice tone="info">
          We sent a new code. Earlier codes no longer work.
        </Notice>
      ) : null}
      {formError ? <Notice tone="danger">{formError}</Notice> : null}
      <form className="auth-form" onSubmit={submitCode} noValidate>
        <TextField
          label="Sign-in code"
          name="code"
          className="auth-code"
          autoComplete="one-time-code"
          inputMode="numeric"
          maxLength={7}
          spellCheck={false}
          value={code}
          onChange={(event) => setCode(event.target.value)}
          error={fieldError}
        />
        <Button type="submit" variant="primary" disabled={busy}>
          {busy ? 'Signing in…' : 'Sign in'}
        </Button>
      </form>
      <div className="auth-actions">
        <Button
          type="button"
          disabled={busy}
          onClick={() => void sendCode(step.email, true)}
        >
          Send a new code
        </Button>
        <Button
          type="button"
          disabled={busy}
          onClick={() => {
            setStep({ kind: 'email' });
            setFieldError(null);
            setFormError(null);
          }}
        >
          Use a different email
        </Button>
      </div>
    </PanelLayout>
  );
}
