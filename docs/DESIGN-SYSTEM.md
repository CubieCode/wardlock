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

| Group          | Tokens                                                                                                                                                                   | Notes                                                            |
| -------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ---------------------------------------------------------------- |
| Surfaces       | `--color-canvas`, `--color-surface`, `--color-surface-muted`                                                                                                             | Canvas is the page; surface is cards and the header              |
| Text           | `--color-text`, `--color-text-muted`, `--color-text-on-primary`                                                                                                          | Muted text still meets 4.5:1 on every surface                    |
| Borders        | `--color-border`, `--color-border-strong`                                                                                                                                | Strong borders are for controls (3:1)                            |
| Actions        | `--color-primary`, `--color-primary-hover`, `--color-accent`, `--color-focus`, `--color-backdrop`                                                                        | Accent (brass) is decorative: the mark and small highlights only |
| Status         | `--color-success`, `--color-warning`, `--color-danger`, each with `-surface` and `-border`                                                                               | Use the three together; always with a word                       |
| Typography     | `--font-sans`, `--font-mono`, `--text-sm` … `--text-2xl`, `--font-weight-*`, `--line-height`                                                                             | System fonts; no web-font requests                               |
| Spacing        | `--space-1` … `--space-12`                                                                                                                                               | 4px grid                                                         |
| Shape and size | `--radius-control`, `--radius-card`, `--radius-pill`, `--focus-ring`, `--touch-target` (44px), `--content-width`, `--sidebar-width`, `--panel-width`, `--duration-short` |                                                                  |

## Themes

`:root` sets `color-scheme: light dark`, so the dashboard follows the system preference with no script. `data-theme="light"` or `"dark"` on `<html>` forces a theme (a future per-device setting would set it). Components never branch on theme.

Rules, enforced by `packages/ui/src/tokens.test.ts`:

- Every `--color-*` role uses `light-dark()` with two hex values.
- The pairs components draw meet WCAG 2.2 AA in both themes: 4.5:1 for text, 3:1 for borders, focus and the accent. Add a new pair to the test when you draw a new combination.
- Colour literals (hex, `rgb()`, `hsl()`, `oklch()`, named colours) appear only in `tokens.css`; the test scans `packages/ui` and `apps/web/src`.

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
| `Fieldset`      | A `legend` over a group of `Radio`s or `Checkbox`es, with optional `hint` and `error` for the group                                                                                           | Native `fieldset`/`legend`; hint and error describe the group; `aria-invalid`; error announced                                    |
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
| `Mark`          | The key-in-ward mark                                                                                                                                                                          | `aria-hidden`                                                                                                                     |
| `cx`            | Joining class names                                                                                                                                                                           |                                                                                                                                   |

A form-level error (the request failed, not one field) is a danger `Notice` above the form; a field error goes in the field's `error`.

Classes are prefixed `wl-`. Screens add their own classes for layout and never restyle a `wl-` class globally.

## Adding to the design system

`packages/ui` is a serialization gate: one lane at a time. Add a component only when at least one screen needs it and it has no domain knowledge. Research the component's common forms on Mobbin first (see [wardlock-web](../.agents/skills/wardlock-web/SKILL.md)) and cite the references in the PR. Give it a test (role, name and states via Testing Library), document it in the table above, and check it in a browser at 1440px and 390px in both themes. Likely next components, added only by the items that need them: toast.
