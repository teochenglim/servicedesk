# Service desk UI redesign, prompt for Claude Code

## Context

This is an internal service desk ticketing system. The current UI is dated and needs a full CSS and layout upgrade to the Thoughtworks brand. Keep all existing functionality and data wiring intact, this is a visual and structural layer change only unless a component genuinely needs new markup to support the new layout.

## Brand tokens

Add these as CSS custom properties in a single `tokens.css` and reference them everywhere, never hardcode hex values in components.

```css
:root {
  /* brand core */
  --tw-teal-900: #033E4E;   /* dark teal, primary bg / headers */
  --tw-teal-600: #48A2AB;   /* mid teal, links, accents, active states */
  --tw-coral-500: #F46277;  /* coral, CTAs, subtitles, highlights */
  --tw-sage-500: #6A9E77;   /* sage, breadcrumbs, section labels, success */
  --tw-amber-500: #CF850C;  /* chart accent, warning */
  --tw-purple-500: #644F7A; /* chart accent, secondary category */

  /* neutrals */
  --tw-white: #FFFFFF;
  --tw-text-on-dark: #FFFFFF;
  --tw-text-on-light: #033E4E;
  --tw-border: rgba(3, 62, 78, 0.12);
  --tw-surface: #FFFFFF;
  --tw-surface-muted: #F4F7F7;

  /* fonts */
  --font-heading: "Bitter", serif;
  --font-body: "Inter", sans-serif;
}
```

Typography:

- H1: Bitter Bold, 28 to 32px, white on dark teal or dark teal on white.
- H2 / subtitle: Bitter Bold, 20 to 24px, coral (`--tw-coral-500`).
- H3 / column and card header: Bitter Bold, 16px, white, sits inside a colored header band.
- Body: Inter regular, 14 to 16px, `--tw-text-on-light` on white or white on dark, line height 1.5.
- Caption / footnote / breadcrumb: Inter, 12px, muted, breadcrumb uses `--tw-sage-500`.

## Important: keep brand color separate from status semantics

Coral and teal are brand accents, not status codes. Do not reuse coral for "urgent" or teal for "resolved" just because they are the brand palette, that will confuse priority and status at a glance. Use this separate semantic set instead:

- Priority: Urgent = red (#D64545), High = `--tw-amber-500`, Normal = `--tw-teal-600`, Low = `--tw-sage-500`.
- Status pill: Open = `--tw-coral-500` fill, Pending = `--tw-amber-500` fill, Resolved = `--tw-sage-500` fill, Closed = neutral gray fill.
- SLA countdown chip: green when on track, amber when approaching breach, red when breached. These stay true red/amber/green regardless of brand palette.

## Desktop layout, three panes

```
┌─────────────────────────────────────────────────────────────┐
│ Top bar (56px, --tw-teal-900 bg, white text)                │
│ logo | global search | notifications, create, agent avatar  │
├──────┬──────────────────┬────────────────────────────────────┤
│ Icon │ Ticket list       │ Ticket detail                     │
│ rail │ (280px)           │ (fills remaining width)           │
│ 48px │                   │  header: id, subject, status,     │
│      │                   │  priority, assignee               │
│      │                   │  conversation thread               │
│      │                   │  reply / internal note composer   │
└──────┴──────────────────┴────────────────────────────────────┘
```

- Top bar: `--tw-teal-900` background, white wordmark, centered search input, right side holds notification bell, a coral "new ticket" button, and agent avatar with status dot.
- Icon rail: `--tw-teal-900` background, muted white icons, active icon gets a 2px coral underline or left bar.
- Ticket list rows: white background, hairline `--tw-border` divider, 4px left bar in the priority color, subject in medium weight, requester name and channel icon below it, SLA chip on the right, unread rows get a coral dot and bold subject.
- Ticket detail header: white background, thin bottom border, status pill and priority badge from the semantic set above, assignee avatar, mid-teal used only for hover and link states here, not fills.

## Conversation thread, WhatsApp style with one addition

- Customer bubble: left aligned, `--tw-surface-muted` background, avatar on the left, rounded corners except the bottom left corner.
- Agent bubble: right aligned, `--tw-teal-600` at 10 percent tint background, `--tw-teal-900` text, rounded corners except the bottom right corner.
- Internal note: full width card, not a bubble, dashed border in `--tw-amber-500`, pale amber background, a small "Internal note" label with an eye off icon at the top of the card. This must look structurally different from both bubble types so an agent cannot mistake it for something the customer will see.
- Timestamps and channel icon (email, chat, phone) sit in small caption text under each bubble.

## Composer

- Segmented control above the input: "Reply" and "Internal note".
- Reply segment active state uses `--tw-teal-600` as the accent underline, the send button uses `--tw-coral-500` fill.
- Internal note segment active state uses `--tw-amber-500` as the accent underline and the whole composer background shifts to the pale amber tint used in the internal note card, so the color itself warns the agent before they type.
- Attach icon and canned response icon sit to the left of the textarea.

## Mobile layout

```
┌───────────────────────┐
│ back  ticket #  ⋮      │  48px header
├───────────────────────┤
│                       │
│  conversation thread   │  same bubble rules, narrower
│  (scrollable)          │
│                       │
├───────────────────────┤
│ [Reply | Internal note]│  sticky composer
│ [ text input   send ]  │
├───────────────────────┤
│ inbox tickets + alerts profile │ 56px bottom tab bar
└───────────────────────┘
```

- Bottom tab bar replaces the icon rail, five icons, active icon in coral, inactive in teal at 60 percent opacity.
- Ticket properties (custom fields, related tickets, asset info) move from a side panel into a bottom sheet, opened from a "Details" chip in the header, not shown inline.
- Composer stays sticky above the safe area, segmented control keeps the same reply versus internal note color logic as desktop.
- Swipe actions on ticket list rows for assign, snooze, and close.

## Spacing, radius, and states

- 8px base spacing grid.
- Radius: 8px on controls and bubbles, 12px on cards.
- Borders: 1px hairline using `--tw-border` at 12 percent opacity, never pure black.
- Hover: `--tw-teal-600` outline or background tint.
- Focus: visible focus ring in `--tw-teal-600`, keep it, do not remove outlines for aesthetics.
- Disabled: reduce opacity, do not desaturate to gray only.

## Deliverables for this session

1. `tokens.css` with the variables above.
2. Updated ticket list component using the priority bar and SLA chip pattern.
3. Updated conversation thread with the three distinct message types (customer, agent, internal note).
4. Updated composer with the segmented reply and internal note control.
5. Responsive breakpoint that swaps the icon rail for the bottom tab bar and turns the properties panel into a bottom sheet on mobile.
6. No em dashes anywhere in UI copy, use a comma or a full stop instead.