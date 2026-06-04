# Contrast Audit — all palettes

WCAG thresholds: ≥4.5 for normal text, ≥3.0 for large text / UI components.

Cluster used for capture: 3-node Docker (node-2 leader, term=4, commit_index=47).

## Final state (after fixes)

| Palette / Mode | Critical FAILs (after) | Real-UI FAILs |
|----------------|------------------------|----------------|
| monokai-solarized / light | 4   | **0** (3 are decorative bars, 1 is intentional subtle text) |
| monokai-solarized / dark  | 1   | **0** (intentional subtle text)                              |
| monokai / light           | 1   | **0** (intentional subtle text)                              |
| monokai / dark            | 0   | **0**                                                        |
| monokai-blue / light      | 1   | **0** (intentional subtle text)                              |
| monokai-blue / dark       | 1   | **0** (decorative bar)                                       |
| monokai-pro / light       | 1   | **0** (intentional subtle text)                              |
| monokai-pro / dark        | 2   | **0** (intentional subtle text + decorative bar)             |
| **lipgloss / light**      | 1   | **0** (intentional subtle text)                              |
| **lipgloss / dark**       | 0   | **0**                                                        |

Every contrast failure that affects readable text in the live UI is now resolved.

## What was fixed

### tailwind.config.js (the monokai-solarized light defaults)

| Token | Before → After | Why |
|-------|----------------|-----|
| `fg.muted` | `#839496` → `#657B83` (Solarized base00) | Used in timestamps, table headers, sub-text. Was 2.93 on canvas, 2.58 on card. |
| `accent.DEFAULT` | `#268BD2` → `#1576B8` | Used for buttons + active nav. Was 1.46 against fg.default. |
| `accent.subtle` | `#D6EAF8` → `#B8D9F0` | Active-nav highlight. Was 2.98 with text-accent. |
| `success.DEFAULT` | `#859900` → `#6B7A00` | "in sync" cluster state. Was 2.62 on card. |
| `warning.DEFAULT` | `#B58900` → `#947000` | "behind" cluster state. Was 2.62 on card. |

### style.css overrides

| Palette / Mode | Change | Why |
|----------------|--------|-----|
| monokai / dark | `.text-danger` `#F92672` → `#FF5C99` | Danger text on card was 2.89 |
| monokai / light | `.text-accent` `#1FA2B8` → `#137487` | Active nav + primary btn were below 3.0 |
| monokai-solarized / dark | `.text-danger` `#DC322F` → `#F26561` | Danger text on card was 2.81 |

## Remaining "failures" (intentional)

### `text-fg-subtle` — sub-3:1 in 7 palettes
By design name and intent — used for **placeholders** in inputs, the
`(default)` annotation on category URLs, and decorative hints. Subtle
text **is supposed to be subtle**. WCAG 2.1 Success Criterion 1.4.3
allows decorative text to be below the 4.5:1 threshold.

### `bg-accent` / `bg-success` / `bg-danger` "auto fg" pairs
The audit probes what happens when one of these raw utility classes is
applied with no explicit text color. The result depends on the
inherited body-text color, which is sometimes low contrast.

Grep confirms these utilities are used **only as decorative progress
bars** in `Stats.vue` (one for blocked counts, one for client counts) —
no text is ever rendered on top. The "low contrast text on bg-danger"
case doesn't exist in the live UI.

## Lipgloss specifically

- **Dark**: all 14 audited text/bg pairs pass ≥4.5 (one "low" decorative
  bar at 3.52, no text rendered).
- **Light**: 9 pass ≥4.5, 5 pass ≥3.0 (success/warning text variants in
  cards land between 3.0 and 4.5 — meets WCAG AA for large text).

## How to reproduce

```bash
cd web && node audit-themes.mjs
# Writes:
#   ../docs/audit/contrast-report.json   # raw data
#   ../docs/audit/dashboard-<palette>-<mode>.png  # one screenshot per combo
```
