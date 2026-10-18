-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only

-- The allowlist of a policy (A2.4, shared with OAuth in A2.5): one list per
-- policy, used by the email-code and OAuth methods alike. Entries are stored
-- normalised (lower case, A-labels), so the same entry has one spelling:
-- - email: an exact address, as name@example.com;
-- - domain: an email domain, example.com (that domain only) or
--   *.example.com (its subdomains, not example.com itself);
-- - github_org: a GitHub organisation login, checked only by OAuth with
--   GitHub (A2.5).
-- The composite key ties each entry to its policy's own site, and deleting
-- the policy (or the site) deletes its allowlist.
CREATE TABLE policy_allowlist (
  policy_id TEXT NOT NULL,
  site_id TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('email', 'domain', 'github_org')),
  value TEXT NOT NULL CHECK (value = lower(value) AND length(value) BETWEEN 1 AND 254),
  PRIMARY KEY (policy_id, kind, value),
  FOREIGN KEY (site_id, policy_id) REFERENCES policies (site_id, id) ON DELETE CASCADE
);

CREATE INDEX policy_allowlist_site_id ON policy_allowlist (site_id);

-- Visitor email codes (A2.4): a 6-digit code sent to an allowed address,
-- valid 10 minutes and 5 attempts, for one policy at one version. Neither
-- the code nor the address is stored: address_hash and code_hash are HMACs
-- keyed by the server key. Sending a new code to the same address for the
-- same policy replaces the previous one, and a code is deleted when it is
-- used, so each is single use.
CREATE TABLE visitor_email_codes (
  id TEXT NOT NULL PRIMARY KEY,
  site_id TEXT NOT NULL,
  policy_id TEXT NOT NULL,
  policy_version INTEGER NOT NULL CHECK (policy_version >= 1),
  address_hash TEXT NOT NULL,
  code_hash TEXT NOT NULL,
  attempts INTEGER NOT NULL CHECK (attempts >= 0),
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  CHECK (expires_at > created_at),
  UNIQUE (site_id, policy_id, address_hash),
  FOREIGN KEY (site_id, policy_id) REFERENCES policies (site_id, id) ON DELETE CASCADE
);

-- Expired codes are deleted by expiry.
CREATE INDEX visitor_email_codes_expires_at ON visitor_email_codes (expires_at);
