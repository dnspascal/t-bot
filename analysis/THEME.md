# Report theme directives

Palette and rules for HTML reports in `analysis/daily/`. Keep new reports consistent with this instead of re-deriving colors each time.

## Palette

| Role | Hex | Used for |
| --- | --- | --- |
| Primary text | `#000` | Headers, body paragraphs, quoted/excerpt text — anything that's primary reading content |
| Secondary text (dark) | `#6b7280` | Nav/meta line, section eyebrows, table headers, code comments, inline captions |
| Secondary text (light) | `#9ca3af` | Footer, citation/source paths, the lightest-weight captions |
| Border (standard) | `#e5e7eb` | Header bottom border, `hr`, card borders, table header bottom border |
| Border (hairline) | `#f0f1f3` | Table row dividers only — lighter than standard border, too heavy otherwise |
| Primary accent | `#1d4ed8` (Tailwind `blue-700`) | Eyebrow badges, links, decision-box title, info chip/badge |
| Win / positive | `#16a34a` text on `#f0fdf4` bg | Win chips, fixed badges, diff `+` lines |
| Loss / negative | `#dc2626` text on `#fef2f2` bg | Loss chips, gap badges |
| Pending / warning | `#c2410c` text on `#fff7ed` bg | Pending badges |

Do not substitute a single gray for both text and borders — they read differently at the same hex and end up either too dark (looks black) or too light (illegible as text). Keep the two roles separate.

## Rules

- **Background is always `#fff`** (page and default card). No dark mode — these are internal one-off reports, not published artifacts.
- **A card gets a border only if its own background is pure white** (`#fff`). Cards with a tinted background (`.quote`, `.decision`, code blocks, chips, badges) get *no* border — the tint alone is enough to separate them from the page. This applies per-card, not globally: check each card's own `background`, not the page's.
- **Font**: Lato (Google Fonts), loaded in `<head>`. Fall back to `system-ui, sans-serif`.
- Code blocks (`pre`) use the light-gray card background (`#f3f4f6`), not a dark/inverted theme — stay consistent with the rest of the page rather than defaulting to a "dark code block" convention.
- Diff-style `+`/`-` lines inside code blocks use the same win/loss greens/reds as the rest of the doc, not separate "syntax highlighting" colors.

## Reference implementation

`analysis/daily/2026-08-13/report.html` is the canonical example — copy its `<style>` block as a starting point for new reports rather than rebuilding from scratch.
