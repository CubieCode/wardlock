---
name: wardlock-publish
description: Publish a directory you or another skill generated (a mockup, prototype, report or any static page) privately on the user's Wardlock server and hand back a share link. Use when the user asks to publish, share, host or preview generated static files with Wardlock, or to list, update or remove pages published that way. It only publishes; it does not generate content.
license: MIT
---

# Publish with Wardlock

Publish a static directory as a private page under one **root site** on the user's own [Wardlock](https://wardlock.nz) server, and give the user a share link that opens only that page. Each page lives at its own path, `/<slug>/`, so many pages share one site and never overwrite each other.

This skill only publishes. Making the content (HTML, a built prototype, a report) is the user's job or another skill's; this skill takes a finished directory with an `index.html` at its top.

Use only the commands in [references/cli.md](references/cli.md), written exactly as there. Pass `--json` on every call and read stdout as one JSON object; on failure stdout is empty, the exit status is not 0 and stderr says why. Write `--json` first, straight after `wardlock`, as the reference does.

## Rules

- **Never public.** A page must never be reachable without a link, not even briefly. Publish only to a root site whose default policy is `protected`; follow "Create a root site" for a new one.
- **Never log in for the user.** If the CLI is not logged in, ask the user to run `wardlock login` themselves.
- **Never overwrite** an existing page unless the user asked to update that page.
- **Share URLs are secrets.** Give the user each link's `url` once, in your reply. Never write it to a file, a commit, a log, a ticket or a command line you echo, and never repeat it later; if it is lost, rotate the link (`wardlock --json share rotate ROOT LINK_ID`).
- Stop and tell the user when a command fails; do not retry with different flags to work around a refusal.

## 1. Check the CLI

1. Run `wardlock --json version`. If the command is not found, tell the user to install the CLI and stop:
   - `curl -fsSLO https://raw.githubusercontent.com/CubieCode/wardlock/main/deploy/install.sh && sh install.sh`, or
   - `go install wardlock.nz/cli/cmd/wardlock@latest`.

   If `version` is older than `0.2.0` (a `dev` build is fine), tell the user to upgrade with the same commands and stop. If a deploy later fails with "does not support deploying into a path", their server is older than v0.2.0: tell them and stop.

2. Run `wardlock --json list`. If it fails with "not logged in", or asks to log in again, ask the user to run `wardlock login --server URL` (their server's address) in their own terminal, then wait. On success, `sites` lists their sites, each with `name`, `url` and `policy`.

## 2. Choose the root site

Use the root site the user names (call it ROOT). If they name none, suggest one, such as `previews`, and confirm it before creating anything.

- **ROOT is in `sites` with `policy` `protected`**: use it.
- **ROOT is in `sites` with `policy` `public`**: stop. Tell the user its pages would be public, and only continue after they have protected it (they can ask you to run `wardlock --json policy set ROOT --protected --links`).
- **ROOT is not in `sites`**: create it as below.

### Create a root site

Do these in order, and do not deploy any real content until step 3 succeeds.

1. Make a temporary directory outside the user's project with one `index.html` holding no content of value (for example `<!doctype html><title>Private</title><p>Nothing to see here.</p>`).
2. Deploy it as the whole site: `wardlock --json deploy PLACEHOLDER_DIR ROOT --message "wardlock-publish: placeholder"`. If the name is taken or invalid, ask the user for another. If the output's `siteCreated` is not `true`, ROOT already existed (it may have been created since you listed the sites): stop here. Do not protect it and never tear it down. Tell the user the site already exists and that the placeholder is now its live deploy (they can restore the previous one with `wardlock rollback ROOT`), then go back to "Choose the root site".
3. Protect it: `wardlock --json policy set ROOT --protected --links`. Add `--password`, `--email` or `--oauth` (with `--allow LIST` or `--github-org LIST`) only when the user asks for those sign-in methods as well; `--links` must always stay on, since this skill hands out links.
4. Confirm it: run `wardlock --json list` and check ROOT's `policy` is `protected`.
5. Delete the temporary directory.

If step 3 or 4 fails, and step 2 of this same run reported `siteCreated: true` for ROOT, run `wardlock --json teardown ROOT --yes` so nothing stays public, and report the error. In any other case report the error and leave the site alone: never tear down a site this run did not create. Never go on to publish a page to a site whose `policy` is not `protected`.

## 3. Publish a page

1. Check DIR has `index.html` at its top. Do not publish a directory holding secrets (`.env` files are never uploaded, but other credentials would be).
2. Choose a slug: lower-case `a-z`, `0-9` and `-`, from the user's wording or DIR's name (`pricing-mockup`). List the pages already published (see "List pages") and, if the slug is taken, add `-2`, `-3` and so on, unless the user asked to update that page.
3. Deploy: `wardlock --json deploy DIR ROOT --path /SLUG/ --message "wardlock-publish: publish /SLUG/"`. Everything else on the site stays live. The output's `path` is `/SLUG/`.
4. Create a link scoped to that page: `wardlock --json share create ROOT --path /SLUG/ --label "wardlock-publish /SLUG/" --expires 7d`. Use the expiry the user asks for (`1h`, `30d`, `never`, ...); default to `7d`.
5. Give the user the output's `url` once, saying which page it opens and when it expires. Do not show the rest of the output: `token` is the same secret.

The link opens only `/SLUG/` and below; it grants nothing on the site's other pages.

## Update a page

Only when the user asks to update a named page that already exists:

`wardlock --json deploy DIR ROOT --path /SLUG/ --message "wardlock-publish: update /SLUG/"`

The page's existing links keep working and now show the new content; every other page is untouched. Create a new link only if the user asks for one. If you no longer have the page's source, get it with "Download a page" first.

## Download a page

When the user asks to update a page whose source you no longer have (it was generated in another session, or its directory was deleted), pull the live page first:

1. Make a new, empty temporary directory outside the user's project (call it PULL_DIR). Never pull into the user's project, or any directory holding their files, without asking them first.
2. Run `wardlock --json pull ROOT PULL_DIR --path /SLUG/`. It writes only that page's files, relative to `/SLUG/`, as "Publish a page" took them, each checked against its SHA-256. Do not add `--force`: PULL_DIR must be empty. The output's `files` and `bytes` count what was written. If the command fails, PULL_DIR may hold only part of the page: never publish from it; stop and report the error.
3. Check PULL_DIR has `index.html` at its top, make the change the user asked for, then publish it back with "Update a page", using PULL_DIR as DIR.
4. Delete PULL_DIR when the update succeeds.

## Remove a page

1. Confirm with the user which page to remove.
2. Deploy an empty directory into its path: make an empty temporary directory and run `wardlock --json deploy EMPTY_DIR ROOT --path /SLUG/ --yes --message "wardlock-publish: remove /SLUG/"`. Only that path is removed.
3. Revoke its links: `wardlock --json share list ROOT`, then for each link in `links` whose `path` is `/SLUG/`, `wardlock --json share revoke ROOT LINK_ID` with the link's `publicId`.

## List pages

1. Run `wardlock --json list ROOT`. `deploys` is newest first; `site.activeDeployId` is the deploy now live.
2. Start at the live deploy and walk to older ones, stopping at the first deploy without a `path` (a whole-site deploy, such as the placeholder). For each `path` you meet for the first time, the page is live unless that deploy's `message` starts with `wardlock-publish: remove`.
3. Run `wardlock --json share list ROOT` to show each page's links: `label`, `publicId`, `expiresAt` and `status`. URLs are never listed.

History keeps the newest 50 deploys of a site, so very old pages may not appear in step 2; ask the user before assuming a slug is free if the history looks truncated.
