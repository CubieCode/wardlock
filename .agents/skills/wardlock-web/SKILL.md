---
name: wardlock-web
description: Design, build or review Wardlock dashboard screens and the packages/ui design system, including Mobbin design research, components, tokens, API access, states, accessibility and the desktop/mobile and light/dark gates.
---

# Dashboard and design system

Read DESIGN.md and docs/DESIGN-SYSTEM.md. For React patterns load [vercel-react-best-practices](../vercel-react-best-practices/SKILL.md); the dashboard is a Vite single-page app, so skip its Next.js, server-component, server-action and hydration rules and use `React.lazy` where it suggests `next/dynamic`.

## Design research with Mobbin

Before designing a new screen or flow, or changing the layout of an existing one, research how established products solve the same job using the Mobbin MCP tools. Design from that evidence, not from memory.

- `search_flows` for multi-step journeys (sign in with an email code, create and copy a share link, roll back a deploy), `search_screens` for single screens (deploy history list, access log table, policy settings), `search_sections` only for the docs or marketing site (O4.3). Use `platform: "web"` for the dashboard and `output_destination: "code"`, with the same `task_intent` for every call in one task.
- One journey or screen per query, described by what is on it ("table of share links with label, expiry and revoke action"), not by style words. Name a product when it is a known reference for the job (for example a hosting dashboard's deploy list). Run several focused queries; look at the images themselves, not just the metadata.
- Pick 3–6 references and note what each contributes: hierarchy, empty and error states, how secrets are shown once and copied, destructive confirmations, mobile layout. Then adapt the pattern to `@wardlock/ui`, DESIGN.md's character and our tokens. Never copy another product's branding, illustrations, copy or colours.
- Record the references in the PR body under **Design references**, each as a markdown link to its `mobbin_url` with one line on what you took from it. Do not commit Mobbin images or link `image_url` (they are licensed and expire after 30 days).
- If the Mobbin tools are not available in your session, say so in the PR and leave the design-research gate open for the coordinator; never skip it silently.

## Building screens

- Build screens from `@wardlock/ui` components. Use semantic tokens (`--color-*`, `--space-*`, `--text-*`) only; colour literals belong in `packages/ui/styles/tokens.css` and a test rejects them elsewhere. A new colour role needs light and dark values in `light-dark()` and its pairs added to the contrast test.
- `packages/ui` is domain-free: no API client, no site, deploy or link concepts. Promote a component there only when it is generic, and only as the wave's `packages/ui` gate owner.
- Talk to the server only through `@wardlock/apiclient-ts`; pass the client into screens so tests can supply a fake `fetch`. ESLint blocks direct `fetch`. Never hand-write API types; regenerate from the spec.
- Every data view has loading, empty, error and success states. Errors say what happened and the next step. Status always includes a word, never colour alone.
- Unbuilt features stay invisible: no placeholder navigation, disabled "coming soon" items or mock data.
- Secrets in the UI: show a raw share-link token once, at creation, with a copy action; afterwards show only its public ID and label. Never put tokens in page titles, logs, analytics or screenshots.
- Accessibility: one `h1` per page, labelled controls, real buttons and links, visible focus, 44px touch targets on mobile, keyboard-completable flows, `prefers-reduced-motion` respected.
- Check every UI change in a browser at 1440px and 390px wide, in light and dark, with no horizontal page scroll; `pnpm browser:test` covers the baseline, extend its specs for new screens.
- Write in NZ English, sentence case, plain verbs ("Create link", "Revoke link"), naming the object in destructive confirmations.
