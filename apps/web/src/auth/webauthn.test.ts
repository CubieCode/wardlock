// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only
import { describe, expect, it } from 'vitest';
import {
  creationOptions,
  deviceName,
  fromBase64url,
  passkeyFailure,
  requestOptions,
  toBase64url,
} from './webauthn.js';

const bytes = (b: ArrayBuffer | BufferSource) =>
  Array.from(new Uint8Array(b as ArrayBuffer));

describe('base64url', () => {
  it('round-trips every byte value without padding', () => {
    const all = new Uint8Array(256).map((_, i) => i);
    const encoded = toBase64url(all);
    expect(encoded).not.toMatch(/[+/=]/);
    expect(bytes(fromBase64url(encoded))).toEqual(Array.from(all));
  });

  it.each([
    ['', []],
    ['AA', [0]],
    ['_-8', [0xff, 0xef]],
  ])('decodes %j', (input, want) => {
    expect(bytes(fromBase64url(input))).toEqual(want);
  });
});

describe('creationOptions', () => {
  it('decodes the challenge, user ID and excluded credential IDs', () => {
    const options = creationOptions({
      rp: { id: 'wardlock.example.com', name: 'Wardlock' },
      user: { id: 'AQI', name: 'o@example.test', displayName: 'o' },
      challenge: 'AwQF',
      pubKeyCredParams: [{ type: 'public-key', alg: -7 }],
      excludeCredentials: [{ type: 'public-key', id: 'Bgc' }],
      attestation: 'none',
    });
    expect(bytes(options.challenge)).toEqual([3, 4, 5]);
    expect(bytes(options.user.id)).toEqual([1, 2]);
    expect(bytes(options.excludeCredentials![0]!.id)).toEqual([6, 7]);
    expect(options.rp).toEqual({
      id: 'wardlock.example.com',
      name: 'Wardlock',
    });
    expect(options.attestation).toBe('none');
  });
});

describe('requestOptions', () => {
  it('decodes the challenge and leaves an absent allow list absent', () => {
    const options = requestOptions({
      challenge: 'AwQF',
      rpId: 'wardlock.example.com',
      userVerification: 'preferred',
    });
    expect(bytes(options.challenge)).toEqual([3, 4, 5]);
    expect('allowCredentials' in options).toBe(false);
    expect(options.rpId).toBe('wardlock.example.com');
  });
});

describe('deviceName', () => {
  it.each([
    [
      'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36',
      'Chrome on macOS',
    ],
    [
      'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36 Edg/141.0.0.0',
      'Edge on Windows',
    ],
    [
      'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1',
      'Safari on iOS',
    ],
    [
      'Mozilla/5.0 (X11; Linux x86_64; rv:140.0) Gecko/20100101 Firefox/140.0',
      'Firefox on Linux',
    ],
    [
      'Mozilla/5.0 (Linux; Android 15; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Mobile Safari/537.36',
      'Chrome on Android',
    ],
    ['curl/8.0', 'Passkey'],
  ])('names %s', (ua, want) => {
    expect(deviceName(ua)).toBe(want);
  });
});

describe('passkeyFailure', () => {
  it('explains a cancelled or timed-out prompt', () => {
    const cancelled = new DOMException('cancelled', 'NotAllowedError');
    expect(passkeyFailure(cancelled, 'sign-in')).toMatch(
      /cancelled or timed out.*email code/,
    );
    expect(passkeyFailure(cancelled, 'add')).toMatch(/cancelled or timed out/);
  });

  it('explains an authenticator that already holds a passkey', () => {
    expect(
      passkeyFailure(new DOMException('exists', 'InvalidStateError'), 'add'),
    ).toMatch(/already has a passkey/);
  });

  it('falls back to a plain message', () => {
    expect(passkeyFailure(new Error('x'), 'sign-in')).toMatch(
      /could not use a passkey/,
    );
  });
});
