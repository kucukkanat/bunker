# Bunker brand kit

**Bunker is a safe place to build.** The brand should feel calm, warm, and dependable. It should say *"safe"* to IT and *"easy"* to everyone else.

## Name
- Always **Bunker** (capital B) in prose; `bunker` (lowercase) in the wordmark, CLI and code.
- Never "BUNKER", "Bunkr", or "the Bunker".

## Logo
| File | Use |
|---|---|
| `logo-mark.svg` | App icon, favicon, avatars. Forest tile, paper arch, clay ground line. |
| `logo-mark-outline.svg` | Single-color use; inherits `currentColor`. |
| `logo-wordmark.svg` | Headers, documents, slides. |
| `logo-mark-512.png` | Places that need a raster. |
| `social-card.svg` | Open Graph / link previews (rendered to `website/public/og.png`). |

The mark is an arched shelter with an open door: protected, but welcoming. The clay line is the ground it stands on.
- Clear space = the door width on every side.
- Minimum size: 16 px (mark), 96 px wide (wordmark).
- Don't rotate, recolor outside the palette, add effects, or put the forest tile on busy imagery.

## Color
| Token | Hex | Use |
|---|---|---|
| Forest 900 | `#1F3A2E` | Primary: logo, headings, primary buttons |
| Forest 700 | `#2F5543` | Hover, links |
| Moss 500 | `#4F7A63` | Secondary accents, icons |
| Moss 200 | `#C9D8CC` | Borders, subtle fills |
| Paper | `#F7F4EC` | Page background |
| Sand | `#E9E3D3` | Cards, sections |
| Ink | `#1C211E` | Body text |
| Slate | `#5A635D` | Secondary text |
| Clay | `#C2683F` | Single accent. Use sparingly (≤ 5% of a layout). |

Dark mode: background `#121714`, surface `#1A211D`, text `#E8EBE6`, accent `#8FBF9F`, clay `#E08A5F`.

All text/background pairs above meet WCAG AA.

## Type
- **Headings:** Fraunces (variable, weight 500–650, slight negative tracking).
- **Body / UI:** Inter (variable, 400/500/600).
- **Code:** JetBrains Mono.

All three are open-source (OFL) and are bundled into the site with Fontsource; nothing loads from third-party CDNs.

## Voice
- **Plain words.** Write for the person in finance, not the engineer. "Share with your team", not "configure ACLs".
- **Calm confidence.** State security facts precisely and don't hype them. "Pages can't reach the internet", not "military-grade security".
- **Short.** One idea per sentence.
- **Honest.** Bunker is in development. Say *planned* when something isn't shipped yet.

Taglines:
- Internal apps anyone can build. IT can trust.
- Describe it. Share it. Safely.
- A safe place to build.
