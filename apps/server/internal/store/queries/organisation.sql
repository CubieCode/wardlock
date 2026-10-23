-- name: ListGroups :many
SELECT id, owner_id, name, note, created_at FROM site_groups
WHERE owner_id = sqlc.arg(owner_id)
ORDER BY name, id;

-- name: GetGroup :one
SELECT id, owner_id, name, note, created_at FROM site_groups
WHERE id = sqlc.arg(id) AND owner_id = sqlc.arg(owner_id);

-- name: CountGroups :one
SELECT COUNT(*) FROM site_groups WHERE owner_id = sqlc.arg(owner_id);

-- name: CreateGroup :exec
INSERT INTO site_groups (id, owner_id, name, note, created_at)
VALUES (sqlc.arg(id), sqlc.arg(owner_id), sqlc.arg(name), sqlc.arg(note), sqlc.arg(created_at));

-- name: UpdateGroup :execrows
UPDATE site_groups SET name = sqlc.arg(name), note = sqlc.arg(note)
WHERE id = sqlc.arg(id) AND owner_id = sqlc.arg(owner_id);

-- Its sites become ungrouped (ON DELETE SET NULL).
-- name: DeleteGroup :execrows
DELETE FROM site_groups WHERE id = sqlc.arg(id) AND owner_id = sqlc.arg(owner_id);

-- name: ListTags :many
SELECT id, owner_id, name, colour, created_at FROM tags
WHERE owner_id = sqlc.arg(owner_id)
ORDER BY name;

-- name: GetTag :one
SELECT id, owner_id, name, colour, created_at FROM tags
WHERE id = sqlc.arg(id) AND owner_id = sqlc.arg(owner_id);

-- name: CountTags :one
SELECT COUNT(*) FROM tags WHERE owner_id = sqlc.arg(owner_id);

-- name: TagColours :many
SELECT colour FROM tags WHERE owner_id = sqlc.arg(owner_id);

-- name: CreateTag :exec
INSERT INTO tags (id, owner_id, name, colour, created_at)
VALUES (sqlc.arg(id), sqlc.arg(owner_id), sqlc.arg(name), sqlc.arg(colour), sqlc.arg(created_at));

-- name: UpdateTag :execrows
UPDATE tags SET name = sqlc.arg(name), colour = sqlc.arg(colour)
WHERE id = sqlc.arg(id) AND owner_id = sqlc.arg(owner_id);

-- Its site_tags rows go with it (ON DELETE CASCADE).
-- name: DeleteTag :execrows
DELETE FROM tags WHERE id = sqlc.arg(id) AND owner_id = sqlc.arg(owner_id);

-- A site's tag IDs in tag-name order. The join on the owner keeps another
-- owner's site empty.
-- name: ListSiteTagIDs :many
SELECT site_tags.tag_id FROM site_tags
JOIN tags ON tags.id = site_tags.tag_id
JOIN sites ON sites.id = site_tags.site_id
WHERE site_tags.site_id = sqlc.arg(site_id) AND sites.owner_id = sqlc.arg(owner_id)
ORDER BY tags.name;

-- name: GetSiteOrganisation :one
SELECT id, group_id, pinned_at FROM sites
WHERE name = sqlc.arg(name) AND owner_id = sqlc.arg(owner_id);

-- Only to a group of the site's own owner, or to none.
-- name: SetSiteGroup :execrows
UPDATE sites SET group_id = sqlc.narg(group_id)
WHERE sites.id = sqlc.arg(site_id) AND sites.owner_id = sqlc.arg(owner_id)
  AND (sqlc.narg(group_id) IS NULL OR EXISTS (
    SELECT 1 FROM site_groups WHERE site_groups.id = sqlc.narg(group_id) AND site_groups.owner_id = sites.owner_id
  ));

-- Pinning a pinned site keeps the first time.
-- name: PinSite :execrows
UPDATE sites SET pinned_at = COALESCE(pinned_at, sqlc.arg(pinned_at))
WHERE id = sqlc.arg(site_id) AND owner_id = sqlc.arg(owner_id);

-- name: UnpinSite :execrows
UPDATE sites SET pinned_at = NULL WHERE id = sqlc.arg(site_id) AND owner_id = sqlc.arg(owner_id);

-- Only when the site and the tag have the same owner; adding a tag the site
-- has changes nothing.
-- name: AddSiteTag :exec
INSERT INTO site_tags (site_id, tag_id)
SELECT sites.id, tags.id FROM sites JOIN tags ON tags.owner_id = sites.owner_id
WHERE sites.id = sqlc.arg(site_id) AND sites.owner_id = sqlc.arg(owner_id) AND tags.id = sqlc.arg(tag_id)
ON CONFLICT (site_id, tag_id) DO NOTHING;

-- name: RemoveSiteTag :exec
DELETE FROM site_tags WHERE site_id = sqlc.arg(site_id) AND tag_id = sqlc.arg(tag_id);

-- name: ClearSiteTags :exec
DELETE FROM site_tags WHERE site_id = sqlc.arg(site_id);

-- name: CountSiteTags :one
SELECT COUNT(*) FROM site_tags WHERE site_id = sqlc.arg(site_id);

-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only
-- (Licence header last: sqlc copies comments above a query into Go docs.)
