---
name: wardlock-mockup
description: Design an HTML mockup of a screen or flow from Mobbin research, check it at desktop and mobile widths in light and dark, publish it privately under the `mockups` Wardlock site and hand back a share link. Use when asked for a mockup, wireframe, prototype or design idea to look at or share, or to update one published before.
metadata:
  internal: true
---

# HTML mockups from Mobbin

Turn a design request into a static HTML mockup grounded in how real products solve the same job, then publish it with a share link that opens only that mockup. The mockup is for looking at and sharing, not for merging: nothing goes into the repository.

## 1. The brief

Work out what to mock up: the screens or steps, the platform (`web`, or `ios` for a phone app), the states that matter (empty, error, loading, filled) and any product it belongs to. Ask only when the request leaves the screen itself unclear; pick sensible defaults for the rest and say what you chose.

For a Wardlock screen, also read DESIGN.md and `packages/ui/styles/tokens.css` and use those tokens' values and DESIGN.md's character, so the mockup looks like the dashboard.

## 2. Research on Mobbin

Use the Mobbin MCP tools before designing; design from that evidence, not from memory.

- `search_flows` for multi-step journeys, `search_screens` for single screens, `search_sections` for marketing or docs page sections. Set `platform` from the brief, `output_destination: "code"`, and the same short `task_intent` on every call of one mockup.
- One screen or journey per query, described by what is on it ("table of invoices with status and download action"), not by style words. Name a product when it is a known reference for the job. Run several focused queries and look at the returned images themselves, not only their metadata.
- Pick 3–6 references and note what each contributes: layout and hierarchy, states, how actions and destructive confirmations are shown, the mobile layout.
- Adapt, never copy: no other product's logo, name, illustrations, copy or brand colours. Never embed or link Mobbin images (`image_url`); they are licensed and expire. Cite references only as `mobbin_url` links.
- If the Mobbin tools are not available, say so and ask whether to continue without research; do not silently skip it.

## 3. Build the mockup

Make a new directory outside the repository and any user project, in the session's scratchpad when there is one (call it DIR). Write:

- `DIR/index.html`: one self-contained page with inline CSS (and inline JavaScript only for interaction the brief needs, such as switching tabs or stepping through a flow). No external requests at all: no CDN scripts, web fonts, analytics or remote images. Use the system font stack, inline SVG for icons and CSS shapes or inline SVG for imagery.
- More pages only when the flow needs them, as `DIR/<step>.html` linked with relative links; or show the steps on one page with a small switcher.

Rules for the content:

- **Synthetic data only.** Names, emails (`@example.com`), numbers and text are invented. Never use real customer content, real email addresses, tokens or anything from `.env` or `.local/`.
- **Responsive and themed.** `<meta name="viewport" content="width=device-width, initial-scale=1">`; colours as CSS custom properties with dark values under `@media (prefers-color-scheme: dark)`; no horizontal scroll at 390px. An `ios` mockup is designed for a 390px-wide viewport; on desktop, centre it in a phone-sized frame.
- **Real states.** Show the states the brief needs, with real-looking copy in NZ English and sentence case. Status is never colour alone.
- **Accessible basics.** One `h1`, real buttons and links, labelled inputs, visible focus, 44px touch targets on mobile.
- Mark it as a mockup in the page `<title>` (`Mockup: Invoice list`), not with a banner over the design.

## 4. Check it

Screenshot every page at desktop and mobile widths in light and dark with the repository's Playwright (install the browser once with `pnpm --filter @wardlock/web exec playwright install chromium`):

```sh
pnpm --filter @wardlock/web exec playwright screenshot --full-page --viewport-size=1440,900 --color-scheme=light file://DIR/index.html SHOTS/desktop-light.png
pnpm --filter @wardlock/web exec playwright screenshot --full-page --viewport-size=390,844 --color-scheme=dark file://DIR/index.html SHOTS/mobile-dark.png
```

Take all four (desktop and mobile, light and dark) into a SHOTS directory beside DIR, never inside it, and look at each image. Fix overflow, clipped text, poor contrast and anything that does not match the references' intent, then check again.

## 5. Publish

Follow [wardlock-publish](../../../skills/wardlock-publish/SKILL.md) exactly, with these choices:

- ROOT is `mockups` unless the user names another site. If it does not exist, the publish skill creates it protected (share links) before anything is deployed; never publish to a site that is not `protected`.
- The slug comes from the mockup's subject (`invoice-list`), with `-2`, `-3` if taken, as that skill says.
- Keep that skill's deploy messages (`wardlock-publish: publish /SLUG/`, and so on), since its page listing relies on them.
- The share link expires in `7d` unless the user asks otherwise.

Only DIR is deployed; the screenshots never are.

## 6. Reply

Tell the user, once:

- the share link `url`, which page it opens and when it expires (never repeat the link later, write it to a file or show the `token`);
- the design references, each as a markdown link to its `mobbin_url` with one line on what you took from it;
- the choices you made where the brief was open, and DIR's location for further changes.

## Updating a mockup

When the user asks for changes to a published mockup, edit DIR (or, if it is gone, get the page back with the publish skill's "Download a page"), check it again as in step 4 and republish with that skill's "Update a page". The existing link keeps working and shows the new version; create a new link only when asked. Research again only when the change is a new screen or a new kind of layout.
