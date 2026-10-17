---
name: wardlock-web
description: Build or review Wardlock dashboard screens and the packages/ui design system: components, tokens, API access, states, accessibility and the desktop/mobile and light/dark gates.
---

# Dashboard and design system

Read DESIGN.md and docs/DESIGN-SYSTEM.md. For React patterns load [vercel-react-best-practices](../vercel-react-best-practices/SKILL.md); the dashboard is a Vite single-page app, so skip its Next.js, server-component, server-action and hydration rules and use `React.lazy` where it suggests `next/dynamic`.

- Build screens from `@wardlock/ui` components. Use semantic tokens (`--color-*`, `--space-*`, `--text-*`) only; colour literals belong in `packages/ui/styles/tokens.css` and a test rejects them elsewhere. A new colour role needs light and dark values in `light-dark()` and its pairs added to the contrast test.
- `packages/ui` is domain-free: no API client, no site, deploy or link concepts. Promote a component there only when it is generic, and only as the wave's `packages/ui` gate owner.
- Talk to the server only through `@wardlock/apiclient-ts`; pass the client into screens so tests can supply a fake `fetch`. ESLint blocks direct `fetch`. Never hand-write API types; regenerate from the spec.
- Every data view has loading, empty, error and success states. Errors say what happened and the next step. Status always includes a word, never colour alone.
- Unbuilt features stay invisible: no placeholder navigation, disabled "coming soon" items or mock data.
- Secrets in the UI: show a raw share-link token once, at creation, with a copy action; afterwards show only its public ID and label. Never put tokens in page titles, logs, analytics or screenshots.
- Accessibility: one `h1` per page, labelled controls, real buttons and links, visible focus, 44px touch targets on mobile, keyboard-completable flows, `prefers-reduced-motion` respected.
- Check every UI change in a browser at 1440px and 390px wide, in light and dark, with no horizontal page scroll; `pnpm browser:test` covers the baseline, extend its specs for new screens.
- Write in NZ English, sentence case, plain verbs ("Create link", "Revoke link"), naming the object in destructive confirmations.
