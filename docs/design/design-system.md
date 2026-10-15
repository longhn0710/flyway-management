# Design system: Flyway Manager — Light Operations

Stitch project: `12852677200871379262`; design system: `assets/12038184256015313756`. This is the canonical Stitch prompt style document, following the StitchDesign DESIGN.md structure. Product-specific requirements override generic creative defaults.

## 1. Visual theme & atmosphere

Restrained, precise Oracle operations console: density 8/10, variance 2/10, motion 1/10. Light default, readable operational evidence, compact navigation, no marketing hero or decorative analytics. Desktop canvas **1440px**. One coherent neutral palette; the explicit user accent takes precedence over the skill's generic saturation cap.

## 2. Color palette & roles

| Token | Value | Role |
|---|---|---|
| canvas | `#F7F8FA` | Page background |
| surface | `#FFFFFF` | Sidebar, forms, tables and dialogs |
| border | `#DDE3E8` | 1px structural boundaries |
| ink | `#172B34` | Primary readable text |
| muted | `#546773` | Secondary text and metadata |
| accent | **`#087E8B`** | Primary action, current navigation, focus |
| accent-soft | `#E5F4F5` | Selected/running background |
| succeeded | `#176B43` / `#EAF6EF` | Success label/background |
| failed | `#B42318` / `#FEECEB` | Failure/error label/background |
| repairing | `#8A5700` / `#FFF4D6` | Separate repair activity |
| blocked | `#8A3D12` / `#FFF0E5` | Unsafe/uncertain stream state |
| queued | `#475467` / `#F2F4F7` | Queued, inactive and cancelled |

Semantic status colors are not competing brand accents. Always pair color with text and an icon. Target contrast: WCAG AA 4.5:1 normal text; 3:1 large text and meaningful UI boundaries. Final browser accessibility testing remains an implementation gate.

## 3. Typography

Geist sans throughout; JetBrains Mono for IDs, versions, timestamps, numbers, checksums and code. Heading 24/32px at 600, section heading 16/24px at 600, body 14/20px, compact table 13/20px, metadata 12/18px. Never shrink primary operational text below 12px. Tabular numbers. Truncate long file paths only with full value available on focus/hover or details. Checksum copy does not reveal credentials.

## 4. Components

Buttons: 36px desktop control, 44px touch target; teal filled primary, neutral outline secondary, red destructive. Explicit labels; one primary action per task region. Visible 2px teal focus outline with 2px offset. Section surfaces have 1px border, 6px radius, no glow; use separators instead of analytics cards.

Forms: labels above inputs, helper below, required marker with textual errors, preserve entered values after validation failure. Dialog ≤640px, bounded content scroll, focus trap, Escape/back with unsaved-work confirmation. Destructive confirmations name resource and consequence. Connection tests distinguish reachability, authentication and probed privileges.

Tables: 40px compact row, clear column labels and sorting, sticky header when useful, selected row distinguishable without color alone; pagination and filtered-count summary. Avoid wrapping numeric IDs unpredictably.

Log viewer: light background, 12px monospace, sequence/time/level/source fields, separate stdout/stderr/event labels, search, level/source filters, follow toggle and download. User scrolling pauses auto-follow with a Resume action.

Credentials: never reveal or copy saved Oracle passwords. Register new secret, rotate by replacement, show provider and metadata only. API token appears once in an explicit issue dialog; never in ordinary table or login. Local encrypted storage default; optional external secret reference.

## 5. Layout and responsive behavior

1440px desktop; 216px white left sidebar, 56px topbar, 24px content margin; spacing 4/8/12/16/24/32px. Shared shell and navigation across all authenticated screens. Review flow uses a clear summary aside. Below 768px sidebar becomes drawer, multi-column forms collapse, tables become summary lists with row detail. No page-level horizontal overflow; keyboard and touch access remain complete. Desktop screenshots do not verify responsive runtime behavior.

## 6. Motion and interaction

Restrained 120–180ms opacity/transform feedback, no decorative perpetual animation. Active run can use subtle activity marker with readable RUNNING label; reduced-motion preference stops animation. Skeletons match final structure. No animated bulk-row cascade in an operational audit table.

## 7. Anti-patterns

No dark default, neon/glow, purple accent, gradient hero, serif, emoji, generic three-card analytics, SQL editor, schema diff, provisioning, approval workflow, manual repair, rollback or SQL retry. No mandatory Vault/Docker/Kubernetes. Do not show 'repair succeeded' as migration success. Never claim schema configuration limits Oracle privileges.
