# Design system (`@wardlock/ui`)

`packages/ui` holds the visual language of every dashboard screen: semantic tokens, base element styles and accessible React components. It is domain-free: no API client, and no site, deploy, link or policy concepts. Domain components (a policy badge, a share-link row) live in `apps/web` and compose these. [DESIGN.md](../DESIGN.md) records intent; this file is the source for what is implemented.

## Using it

The dashboard imports the stylesheet once in `apps/web/src/main.tsx`:

```ts
import '@wardlock/ui/styles.css';
```

Components come from the package root: `import { AppShell, Button, Card } from '@wardlock/ui';`. The package builds with `tsc`; `pnpm dev` and `pnpm build` build it first.

## Tokens (`styles/tokens.css`)

Use semantic tokens in all CSS. Every colour role is written once as `light-dark(<light>, <dark>)`.

| Group              | Tokens                                                                                                                                                   | Notes                                                                                                                                                      |
| ------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Canvas             | `--color-canvas`, `--color-canvas-dot`, `--dot-grid-size`                                                                                                | The page, drawn by `base.css` with a dot grid; the dots are decorative                                                                                     |
| Surfaces           | `--color-surface`, `--color-surface-muted`                                                                                                               | Surface is panels, cards and the sidebar; muted is wells, inputs and hovers                                                                                |
| Text               | `--color-text`, `--color-text-muted`, `--color-text-on-primary`                                                                                          | Muted text still meets 4.5:1 on every surface                                                                                                              |
| Borders            | `--color-border`, `--color-border-strong`                                                                                                                | Strong borders are for controls (3:1)                                                                                                                      |
| Primary            | `--color-primary`, `--color-primary-hover`, `--color-primary-soft`, `--color-primary-glow`, `--glow`                                                     | Violet: actions, links, the current page. Soft is a selected fill under primary text; `--glow` is the one halo (current nav item, primary button, dialogs) |
| Ward and public    | `--color-ward`, `--color-public`, each with `-surface` and `-border`                                                                                     | Brass marks protection (the mark, the ward diamond); coral marks public. Always beside a word                                                              |
| Status             | `--color-success`, `--color-warning`, `--color-danger`, `--color-neutral`, each with `-surface` and `-border`                                            | Use the three together; always with a word                                                                                                                 |
| Focus and backdrop | `--color-focus`, `--color-backdrop`, `--focus-ring`                                                                                                      | Backdrop is drawn at partial opacity behind a modal                                                                                                        |
| Tags               | `--color-tag-0` … `--color-tag-8`                                                                                                                        | 0 is neutral; 1–8 are blue, orange, aqua, yellow, magenta, green, violet, red, in that fixed order. A tag's colour is always beside its name               |
| Charts             | `--color-chart-allowed`, `--color-chart-refused`                                                                                                         | Requests allowed and refused; charts also carry a legend and a table view                                                                                  |
| Typography         | `--font-sans`, `--font-mono`, `--text-xs` … `--text-2xl`, `--font-weight-*`, `--line-height`, `--letter-spacing-caps`                                    | System fonts; no web-font requests. Mono for names, hostnames and IDs; caps spacing for small mono labels such as table headers                            |
| Spacing            | `--space-1` … `--space-12`                                                                                                                               | 4px grid                                                                                                                                                   |
| Shape and size     | `--radius-control`, `--radius-card`, `--radius-pill`, `--touch-target` (44px), `--content-width`, `--sidebar-width`, `--panel-width`, `--duration-short` |                                                                                                                                                            |

## Themes

`:root` sets `color-scheme: light dark`, so the dashboard follows the system preference with no script. Dark is the reference look and light has equal standing. `data-theme="light"` or `"dark"` on `<html>` forces a theme by setting `color-scheme`, which every `light-dark()` role follows (the theme setting sets it). Components never branch on theme.

Colour literals belong only in `tokens.css`, and every drawn pair meets WCAG 2.2 AA in both themes.

<details>
<summary>Rules enforced by <code>tokens.test.ts</code></summary>

Rules, enforced by `packages/ui/src/tokens.test.ts`:

- Every `--color-*` role uses `light-dark()` with two hex values.
- The pairs components draw meet WCAG 2.2 AA in both themes: 4.5:1 for text (including the status, ward and public words on their surfaces and on plain surfaces), 3:1 for control borders, focus, tag dots and chart marks on the canvas and both surfaces. Add a new pair to the test when you draw a new combination.
- Tag slots 1–8 and the chart pair pass the dataviz palette checks in both themes: OKLCH lightness inside the theme's band, chroma at least 0.1, adjacent slots at least 8 apart (OKLab ΔE×100) under simulated protanopia and deuteranopia, and at least 15 apart for full-colour vision. The slot order is part of the check; never reorder or add a slot without re-running it.
- Colour literals (hex, `rgb()`, `hsl()`, `oklch()`, named colours) appear only in `tokens.css`; the test scans `packages/ui` and `apps/web/src`.

</details>

## Components

| Component       | Use for                                                                                                                                                                                       | Accessibility built in                                                                                                            |
| --------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------- |
| `AppShell`      | Signed-in frame: sidebar with wordmark, `navigation` and `account`; a top bar with a Menu button below 48rem; one `main`                                                                      | Landmarks; Menu button has `aria-expanded`/`aria-controls`; following a link closes the menu                                      |
| `PanelLayout`   | Single-task frame (sign-in, setup): wordmark over one centred panel, one `main`                                                                                                               | Landmark; wordmark is a real link                                                                                                 |
| `NavList`       | Labelled navigation list of `NavItem`s                                                                                                                                                        | `nav` with `aria-label`                                                                                                           |
| `NavItem`       | One navigation link; `current` marks the page shown                                                                                                                                           | `aria-current="page"`, 44px target                                                                                                |
| `TextField`     | Labelled text input with optional `hint` and `error`                                                                                                                                          | Visible label; hint and error linked by `aria-describedby`; `aria-invalid`; error announced (`role="alert"`)                      |
| `SelectField`   | Labelled native select with optional `hint` and `error`; `children` are its `<option>`s                                                                                                       | Same as `TextField`: visible label, hint and error linked by `aria-describedby`, `aria-invalid`, error announced                  |
| `TextAreaField` | Labelled multi-line text (lists entered one per line) with optional `hint` and `error`                                                                                                        | Same as `TextField`                                                                                                               |
| `Fieldset`      | A `legend` over a group of `Radio`s or `Checkbox`es, with optional `hint` and `error` for the group                                                                                           | Native `fieldset`/`legend`; hint and error describe the group (no `aria-invalid`, which a group cannot take); error announced     |
| `Checkbox`      | One option that can be on or off, with optional `hint`                                                                                                                                        | Native checkbox; the label beside it is the 44px target; hint linked by `aria-describedby`                                        |
| `Radio`         | One of a set of exclusive options sharing a `name`, inside a `Fieldset`; optional `hint`                                                                                                      | Same as `Checkbox`; arrow keys move within the set                                                                                |
| `PageHeader`    | The page's single `h1` and a short description                                                                                                                                                | Heading level fixed at 1                                                                                                          |
| `Card`          | A titled section of related content                                                                                                                                                           | `section` labelled by its title; `h2` heading                                                                                     |
| `Button`        | Actions. `type` is required; `variant`: `primary` (one per area), `secondary`, or `danger` (only the confirming button of a destructive action, inside its `Dialog`)                          | Native button, focus ring, 44px minimum height                                                                                    |
| `StatusBadge`   | A short status word; `tone`: `neutral`, `success`, `warning`, `danger`; optional `icon` glyph so states also differ in shape                                                                  | Text carries the meaning, colour and icon supplement it (icon is `aria-hidden`)                                                   |
| `Notice`        | Inline guidance; `tone`: `info`, `warning`, `danger`                                                                                                                                          | `role="status"`, or `role="alert"` for danger                                                                                     |
| `Dialog`        | A modal confirmation on the native `<dialog>`: `title` names the object, children explain the consequences, `actions` holds Cancel then the action; the caller owns `open`                    | Named by its title, described by its body; page behind is inert; Escape calls `onClose`; focus moves in and returns to the opener |
| `CopyField`     | A labelled read-only value (an address, a command) with a Copy button                                                                                                                         | Button named "Copy <label>"; result announced in a `role="status"` line; selects the value when the clipboard is unavailable      |
| `Table`         | Records read side by side (log entries, histories): `caption`, `columns` (`header`, `cell`, optional `mono` and `nowrap`), `rows`, `rowKey`. Below 48rem each row stacks into labelled values | Captioned table (caption visually hidden) with column headers; explicit table roles keep rows and headers when stacked            |
| `EmptyState`    | What an empty list would hold and, optionally, the next step                                                                                                                                  | `h2` title                                                                                                                        |
| `Mark`          | The mark: a keyhole in the brass ward diamond                                                                                                                                                 | `aria-hidden`                                                                                                                     |
| `cx`            | Joining class names                                                                                                                                                                           |                                                                                                                                   |

A form-level error (the request failed, not one field) is a danger `Notice` above the form; a field error goes in the field's `error`.

Classes are prefixed `wl-`. Screens add their own classes for layout and never restyle a `wl-` class globally.

## Adding to the design system

`packages/ui` is a serialization gate: one lane at a time. Add a component only when at least one screen needs it and it has no domain knowledge. Research the component's common forms on Mobbin first (see [wardlock-web](../.agents/skills/wardlock-web/SKILL.md)) and cite the references in the PR. Give it a test (role, name and states via Testing Library), document it in the table above, and check it in a browser at 1440px and 390px in both themes. Likely next components, added only by the items that need them: toast.
