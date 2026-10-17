// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Moves WebAuthn values between the API's JSON (binary members as base64url
// without padding) and the browser's credential API (ArrayBuffers). Written
// out rather than relying on PublicKeyCredential.parse*FromJSON and toJSON,
// which not every supported browser has yet.

import type { components } from '@wardlock/apiclient-ts';

type Options = Record<string, unknown>;

/** Whether this browser can create and use passkeys at all. */
export function passkeysSupported() {
  return (
    typeof window !== 'undefined' &&
    typeof window.PublicKeyCredential === 'function' &&
    typeof navigator.credentials?.create === 'function' &&
    typeof navigator.credentials.get === 'function'
  );
}

export function fromBase64url(value: string): ArrayBuffer {
  const base64 = value.replace(/-/g, '+').replace(/_/g, '/');
  const binary = atob(base64.padEnd(Math.ceil(base64.length / 4) * 4, '='));
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  return bytes.buffer;
}

export function toBase64url(value: ArrayBuffer | ArrayBufferView): string {
  const bytes =
    value instanceof ArrayBuffer
      ? new Uint8Array(value)
      : new Uint8Array(value.buffer, value.byteOffset, value.byteLength);
  let binary = '';
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary)
    .replace(/\+/g, '-')
    .replace(/\//g, '_')
    .replace(/=+$/, '');
}

interface DescriptorJSON {
  type: PublicKeyCredentialType;
  id: string;
  transports?: AuthenticatorTransport[];
}

/** Decodes a credential descriptor list's IDs; absent stays absent. */
function withDescriptors<T extends object>(
  options: T,
  key: 'excludeCredentials' | 'allowCredentials',
  list: unknown,
): T {
  if (!Array.isArray(list)) return options;
  return {
    ...options,
    [key]: (list as DescriptorJSON[]).map((d) => ({
      ...d,
      id: fromBase64url(d.id),
    })),
  };
}

/** Registration options from startPasskeyRegistration, for credentials.create. */
export function creationOptions(
  options: Options,
): PublicKeyCredentialCreationOptions {
  const json = options as unknown as PublicKeyCredentialCreationOptions & {
    challenge: string;
    user: PublicKeyCredentialUserEntity & { id: string };
  };
  return withDescriptors(
    {
      ...json,
      challenge: fromBase64url(json.challenge),
      user: { ...json.user, id: fromBase64url(json.user.id) },
    },
    'excludeCredentials',
    json.excludeCredentials,
  );
}

/** Sign-in options from startPasskeySignIn, for credentials.get. */
export function requestOptions(
  options: Options,
): PublicKeyCredentialRequestOptions {
  const json = options as unknown as PublicKeyCredentialRequestOptions & {
    challenge: string;
  };
  return withDescriptors(
    { ...json, challenge: fromBase64url(json.challenge) },
    'allowCredentials',
    json.allowCredentials,
  );
}

/** The API's JSON form of a credential returned by create or get. */
export type CredentialJSON = components['schemas']['PasskeyCredential'];

export function credentialJSON(
  credential: PublicKeyCredential,
): CredentialJSON {
  const r = credential.response;
  const response: Record<string, unknown> = {
    clientDataJSON: toBase64url(r.clientDataJSON),
  };
  if ('attestationObject' in r) {
    const attestation = r as AuthenticatorAttestationResponse;
    response.attestationObject = toBase64url(attestation.attestationObject);
    if (typeof attestation.getTransports === 'function')
      response.transports = attestation.getTransports();
  } else {
    const assertion = r as AuthenticatorAssertionResponse;
    response.authenticatorData = toBase64url(assertion.authenticatorData);
    response.signature = toBase64url(assertion.signature);
    if (assertion.userHandle)
      response.userHandle = toBase64url(assertion.userHandle);
  }
  const json: CredentialJSON = {
    id: credential.id,
    rawId: toBase64url(credential.rawId),
    type: 'public-key',
    response,
    clientExtensionResults: {
      ...(credential.getClientExtensionResults() as Record<string, unknown>),
    },
  };
  if (credential.authenticatorAttachment)
    json.authenticatorAttachment = credential.authenticatorAttachment;
  return json;
}

/**
 * Why the browser's passkey prompt ended without a credential, in words the
 * owner can act on. `NotAllowedError` covers both cancelling and timing out;
 * browsers do not say which.
 */
export function passkeyFailure(error: unknown, action: 'sign-in' | 'add') {
  const name = error instanceof DOMException ? error.name : '';
  if (name === 'NotAllowedError' || name === 'AbortError')
    return action === 'sign-in'
      ? 'Passkey sign-in was cancelled or timed out. Try again, or sign in with an email code.'
      : 'Adding the passkey was cancelled or timed out. Try again when you are ready.';
  if (name === 'InvalidStateError')
    return 'This device or password manager already has a passkey for your account.';
  return action === 'sign-in'
    ? 'This browser could not use a passkey here. Sign in with an email code instead.'
    : 'This browser could not create a passkey here.';
}

/**
 * A starting name for a new passkey, from the browser and system that made
 * it, such as "Chrome on macOS". Owners mostly add a passkey from the device
 * that will hold it.
 */
export function deviceName(userAgent: string) {
  const browser = /Edg\//.test(userAgent)
    ? 'Edge'
    : /Firefox\//.test(userAgent)
      ? 'Firefox'
      : /Chrome\//.test(userAgent)
        ? 'Chrome'
        : /Safari\//.test(userAgent)
          ? 'Safari'
          : null;
  const system = /iPhone|iPad/.test(userAgent)
    ? 'iOS'
    : /Android/.test(userAgent)
      ? 'Android'
      : /Mac OS X|Macintosh/.test(userAgent)
        ? 'macOS'
        : /Windows/.test(userAgent)
          ? 'Windows'
          : /CrOS/.test(userAgent)
            ? 'ChromeOS'
            : /Linux/.test(userAgent)
              ? 'Linux'
              : null;
  if (browser && system) return `${browser} on ${system}`;
  return browser ?? system ?? 'Passkey';
}
