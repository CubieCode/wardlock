# Wardlock design direction

## The name

A warded lock has fixed internal obstructions, called wards, and only a key cut to match them can turn. Wardlock works the same way: each site has its wards (its access policy), and a share link, password, email code or sign-in is a key cut for that site and nothing else. Say "ward-lock", never "warlock". No wizards, runes or spell imagery.

## Character

Calm, trustworthy and plain. People use Wardlock to hand something to a client, a colleague or an AI tool and be confident only the right people can open it. The product should feel like a well-made lock: solid, quiet, obvious to operate. It is not a "hacker" tool: no terminal-green palettes, skulls, glitch effects, padlock-emoji overload or security jargon in the interface.

[DESIGN-SYSTEM](docs/DESIGN-SYSTEM.md) owns the implemented tokens and components; [SPEC](docs/SPEC.md) owns behaviour. This file is intent, not a second palette.

## Colour and type

Deep ink text on a soft neutral canvas, a dark teal for decisive actions, and a brass accent taken from a key, used sparingly (the mark, small highlights), never as a warning colour or button fill. Success, warning and danger each pair text, surface and border, and always come with a word: Public, Protected, Expired, Revoked. Light and dark themes follow the system preference with the same identity. Contrast meets WCAG 2.2 AA on every drawn pair; a test enforces it.

System fonts only, so the dashboard makes no third-party requests. Monospace for tokens' public IDs, hashes, hostnames and commands.

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
