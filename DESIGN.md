# Wardlock design direction

## The name

A warded lock has fixed internal obstructions, called wards, and only a key cut to match them can turn. Wardlock works the same way: each site has its wards (its access policy), and a share link, password, email code or sign-in is a key cut for that site and nothing else. Say "ward-lock", never "warlock". No wizards, runes or spell imagery.

## Character

Calm, trustworthy and plain, drawn as a constellation. People use Wardlock to hand something to a client, a colleague or an AI tool and be confident only the right people can open it. The dashboard shows that as a map they can read at a glance: the server is a hub, groups and sites hang off it on dashed wires, and every site carries a small mark that says who can open it. The product should still feel like a well-made lock: solid, quiet, obvious to operate. It is not a "hacker" tool: no terminal-green palettes, skulls, glitch effects, padlock-emoji overload or security jargon in the interface.

<details>
<summary>What Constellation means in practice</summary>

- **Dark first, light equal.** The reference look is a near-black canvas with a faint dot grid; the light theme is the same design on a pale canvas, never an afterthought. Both meet the same contrast rules.
- **The hub and its wires.** Where a screen shows structure (the server, its groups, their sites), the server is drawn as a hub node and children hang off it on dashed connectors. Below the mobile breakpoint the wires collapse to plain indentation.
- **The ward.** A small brass diamond marks protection, echoing the mark. It is decorative: the policy word beside it carries the meaning.
- **Restraint.** One soft violet glow marks the current place or the main action; everything else stays flat.

</details>

[DESIGN-SYSTEM](docs/DESIGN-SYSTEM.md) owns the implemented tokens and components; [SPEC](docs/SPEC.md) owns behaviour. This file is intent, not a second palette.

## Research before designing

New screens and flows start from evidence: the [wardlock-web](.agents/skills/wardlock-web/SKILL.md) skill has designers and agents search Mobbin for how established products handle the same job (share dialogs, deploy histories, access logs, sign-in), then adapt the pattern to Wardlock's own character and tokens. References are cited in the PR as Mobbin links. Borrow structure and behaviour, never another product's branding, copy or illustrations.

## Colour and type

| Role    | Colour                                 | Used for                                                                                    |
| ------- | -------------------------------------- | ------------------------------------------------------------------------------------------- |
| Primary | Violet, with a soft fill and a glow    | Actions, links, focus of attention: the current page, the main button                       |
| Ward    | Brass                                  | Protection: the diamond beside a protected site, the mark. Never a warning or a button fill |
| Public  | Coral                                  | Marking a site or path anyone can open                                                      |
| Status  | Success, warning, danger, neutral      | Each pairs text, surface and border, and always comes with a word: Live, Expired, Revoked   |
| Tags    | Neutral plus eight categorical colours | A tag's dot, always beside its name                                                         |
| Charts  | Allowed and refused                    | Request charts, always with a legend and a table view                                       |

Light and dark themes follow the system preference with the same identity, and a theme setting can force either. Contrast meets WCAG 2.2 AA on every drawn pair; a test enforces it.

System fonts only, so the dashboard makes no third-party requests. Monospace for site names, hostnames, IDs, hashes and commands; the sans face for everything else.

<details>
<summary>Why these colours</summary>

- Violet is distinct from every status colour, so an action never reads as a state.
- Brass comes from a key and stays small; coral is warm enough to say "open to anyone" without reading as an error. Both always sit beside a word ("Protected", "Public").
- The eight tag colours and the chart pair are checked for colour-blind separation (protanopia and deuteranopia) and for full-colour separation in both themes, in a fixed order; a ninth colour is never generated.

</details>

## Layout and components

One clear task per screen, with the most important fact first: a site's address and its policy, a link's label and whether it still works. Compact lists for repeated records, generous space around consequential actions such as revoking a link or rolling back a deploy. Cards group related content; do not wrap every value in its own card.

## Writing

NZ English, sentence case, plain verbs: "Create link", "Revoke link", "Roll back to this deploy". Explain consequences before destructive actions and name the object ("Revoke the link labelled 'sent to Alice'? Anyone using it loses access immediately."). Never show a raw token after creation; refer to links by label and public ID. Avoid claims of absolute security.

## Do and don't

- **Do** make a site's current policy visible wherever the site appears.
- **Do** show what a share link grants (site, path, expiry, uses left) next to the link.
- **Do** keep pages fast and light; sites served by Wardlock carry no Wardlock branding unless a login page is required.
- **Don't** show unbuilt features, placeholder navigation or sample data.
- **Don't** rely on colour, icons or position alone to show state.
