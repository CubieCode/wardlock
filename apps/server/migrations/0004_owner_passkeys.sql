-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only

-- Owner passkeys (C1.6): WebAuthn credentials for dashboard sign-in, with
-- the dashboard host as the relying party.
-- - credential_id is the authenticator's credential ID, base64url without
--   padding (at most 1023 bytes, so 1364 characters). It is unique across
--   the instance because discoverable sign-in finds the passkey, and so the
--   owner, from it.
-- - credential is the WebAuthn credential record as JSON: public key, sign
--   count, flags, transports and AAGUID. None of it is secret.
-- - name is the owner's label for it, shown in the dashboard.
CREATE TABLE owner_passkeys (
  id TEXT NOT NULL PRIMARY KEY,
  owner_id TEXT NOT NULL REFERENCES owners (id) ON DELETE CASCADE,
  credential_id TEXT NOT NULL UNIQUE CHECK (length(credential_id) BETWEEN 1 AND 1364),
  credential TEXT NOT NULL CHECK (length(credential) > 0),
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
  created_at INTEGER NOT NULL,
  last_used_at INTEGER
);

CREATE INDEX owner_passkeys_owner ON owner_passkeys (owner_id, created_at);
