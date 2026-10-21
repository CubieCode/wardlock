-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only

-- Path deploys (C1.13). A deploy created with a path prefix replaces only
-- that prefix of the site: its manifest is the base deploy's files outside
-- the prefix plus the uploaded files under it, so every deploy is still a
-- complete snapshot and rollback is unchanged. path_prefix is NULL for a
-- full deploy, otherwise a clean prefix such as '/mockup-x/' (never '/').
ALTER TABLE deploys ADD COLUMN path_prefix TEXT CHECK (path_prefix IS NULL OR (path_prefix LIKE '/%/' AND length(path_prefix) BETWEEN 3 AND 1024));

-- The site's active deploy when the path deploy was created: activation
-- refuses it unless the site still serves that deploy, so two concurrent
-- path deploys never drop each other's files. NULL for a full deploy and
-- for a path deploy to a site with nothing live, which then activates only
-- while the site still has nothing live. A base that garbage collection
-- later deletes is no longer active, so clearing the reference keeps a
-- pending path deploy refused.
ALTER TABLE deploys ADD COLUMN base_deploy_id TEXT REFERENCES deploys (id) ON DELETE SET NULL;
