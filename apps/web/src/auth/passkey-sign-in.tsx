// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import { Button, Notice } from '@wardlock/ui';
import { useState } from 'react';
import { navigate } from '../shell/router.js';
import { failure, unreachable } from './messages.js';
import { useSession } from './session.js';
import {
  credentialJSON,
  passkeyFailure,
  passkeysSupported,
  requestOptions,
} from './webauthn.js';

/**
 * "Sign in with a passkey", offered under the email form when the browser
 * supports passkeys. It names no account: the browser offers whichever of
 * this dashboard's passkeys it holds. `disabled` is set while the email form
 * is busy.
 */
export function PasskeySignIn({
  next,
  disabled,
}: {
  next: string;
  disabled: boolean;
}) {
  const { api, signedIn } = useSession();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  if (!passkeysSupported()) return null;

  const signIn = async () => {
    setBusy(true);
    setError(null);
    try {
      const start = await api.POST('/api/v0/auth/passkey');
      if (!start.data) {
        setError(failure(start.response));
        return;
      }
      let credential: Credential | null;
      try {
        credential = await navigator.credentials.get({
          publicKey: requestOptions(start.data.options),
        });
      } catch (err) {
        setError(passkeyFailure(err, 'sign-in'));
        return;
      }
      if (!(credential instanceof PublicKeyCredential)) {
        setError(passkeyFailure(null, 'sign-in'));
        return;
      }
      const {
        data,
        error: apiError,
        response,
      } = await api.POST('/api/v0/auth/passkey/verify', {
        body: {
          ceremonyId: start.data.ceremonyId,
          credential: credentialJSON(credential),
        },
      });
      if (data) {
        signedIn(data);
        navigate(next, { replace: true });
        return;
      }
      setError(
        apiError?.code === 'invalid_passkey'
          ? 'That passkey was not accepted. It may have been removed from your account. Sign in with an email code instead.'
          : failure(response),
      );
    } catch {
      setError(unreachable);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="auth-alternative">
      <p className="auth-divider" aria-hidden="true">
        or
      </p>
      {error ? <Notice tone="danger">{error}</Notice> : null}
      <Button
        type="button"
        disabled={busy || disabled}
        onClick={() => void signIn()}
      >
        {busy ? 'Waiting for your passkey…' : 'Sign in with a passkey'}
      </Button>
    </div>
  );
}
