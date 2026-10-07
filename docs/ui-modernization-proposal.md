# Web console modernization proposal

## Status

Proposed for approval. This document defines the next web-console
modernization increment; it does not change the application behaviour or
privacy/security requirements defined elsewhere.

## Purpose

The current single-page console exposes every feature as a continuous page and
gives configuration, operational work, and research/navigation equal visual
weight. The modernization should make the common operator tasks easy to find,
keep infrastructure details available without foregrounding them, and provide
a polished accessible interface without adding a large component framework.

The intended character is a restrained technical instrument: calm, legible,
and practical for long-running research work rather than a dense dashboard.

## Information architecture

The application shell has a compact header containing the application name,
primary navigation, a theme control, and a settings menu.

Primary destinations are:

- **Index** — create a scan, view the active/recent scan, and pause, resume,
  or cancel it.
- **Search** — search stored headers and inspect an article.
- **Coverage** — browse timeline coverage, stored groups, retention history,
  and chronological stored headers.
- **Watchlist** — manage recurring retention checks and inspect their results.

Provider endpoint configuration, connection limits, quotas, and qualification
history belong in **Settings → Providers**. This keeps a back-end concern out
of ordinary research workflows. It does not hide provider work from an active
scan: the Index view presents safe, endpoint-labelled job status rows only.

The first release may use client-side view selection rather than routing. Each
view must have a stable, directly linkable URL before the application claims
that navigation state can be shared or bookmarked.

## Active scan and provider progress

The Index view contains an active-scan card above the new-scan form. It shows
the newsgroup, overall state, scan reason when relevant, total headers
retrieved, total articles stored, transfer usage where capped, and available
job controls.

Below the summary, each provider child job is displayed as a compact progress
row:

- endpoint label;
- state badge (queued, running, paused, interrupted, completed, failed, or
  cancelled);
- an indeterminate progress indicator while the provider is working;
- headers retrieved and articles stored; and
- a concise error disclosure when present.

The current API deliberately provides `state`, `headers_retrieved`, and
`articles_stored`, but no reliable total work estimate. Therefore this release
MUST NOT render a fabricated percentage or determinate completion bar. A
determinate bar can be introduced only when the backend supplies a documented,
honest estimate or total per provider (including its uncertainty/meaning).

Provider details in this panel must never include credentials or other secret
configuration. This continues the existing safe provider metadata boundary.

## Visual system and themes

Introduce a small CSS-token design system, kept in the frontend source rather
than importing a UI component library. Tokens cover:

- page and elevated surfaces;
- primary and muted text;
- borders and focus rings;
- one restrained brand accent (indigo or teal, selected during implementation);
- semantic success, warning, danger, and informational colours; and
- shared spacing, radius, shadow, and motion values.

Provide `light`, `dark`, and `system` theme settings. System is the initial
default and follows `prefers-color-scheme`; an explicit choice is persisted in
browser storage. Theme changes apply before first paint where practical to
avoid a bright/dark flash.

The layout uses a comfortably constrained reading width for forms and article
text, with wider responsive regions for tables. Cards separate active work
from secondary actions. Tables retain semantic markup on wide screens and
remain usable on narrow screens through horizontal containment or a
purpose-designed compact representation.

## Interaction and accessibility requirements

- Use real buttons, labels, fieldsets, tables, headings, and live regions;
  styling must not replace semantics.
- Preserve a clearly visible keyboard focus indicator and logical tab order.
- Do not convey state through colour alone: every colour-coded state has text
  and, where useful, an icon with an accessible name.
- Respect `prefers-reduced-motion`; avoid nonessential animation and do not
  animate progress in a way that impairs reading.
- Scope busy/loading state to the action or panel that initiated it where
  possible. A provider refresh must not disable an unrelated search action.
- Put errors adjacent to their initiating context and retain enough text for
  recovery. Continue exposing important asynchronous job changes through an
  appropriate polite live region.
- Article inspection should open in a side panel or modal that returns focus
  to its invoking search result when closed. It must not make a network request
  for body text until the existing explicit retrieval action is chosen.

## Delivery plan

### Increment 1: foundations and app shell

1. Split the current inline styling into global CSS and semantic component
   classes; add CSS tokens and light/dark/system theme support.
2. Add the persistent header and client-side primary-view selection.
3. Keep all existing API calls and behaviours working while placing the current
   content into Index, Search, Coverage, Watchlist, and Settings views.
4. Add frontend tests for initial theme selection, persistence, and basic
   keyboard-accessible navigation.

### Increment 2: Index workspace

1. Build the active-scan card and provider child-job rows.
2. Use indeterminate native or ARIA-compliant progress indicators for running
   providers, with state/count text.
3. Move endpoint inventory and qualification history to Settings → Providers.
4. Test rendering for every job/provider state and confirm polling still stops
   at terminal job states.

### Increment 3: focused research workflows

1. Rebuild Search as a focused results workspace with scoped loading/error
   feedback and an accessible article-detail panel.
2. Combine coverage, storage, retention, and chronology into the Coverage
   workspace without changing their NNTP-request semantics.
3. Rebuild Watchlist as its own focused management view.
4. Validate desktop and narrow viewport layouts manually, in addition to the
   automated test suite.

## Acceptance criteria

- A first-time visitor sees the system theme; an explicit theme selection
  persists through reload.
- The console has the four primary workflows listed above and provider
  administration is not part of the routine Index workspace.
- An active fan-out job displays a live row per provider with state and count
  information, and uses an indeterminate—not misleading percentage—indicator
  while work has no known total.
- Existing job creation/control, search, unwanted marking, explicit body
  retrieval, timeline completion, retention probing, and watchlist behaviour
  remain available and retain their existing safety constraints.
- Core workflows are usable with keyboard-only navigation and in both colour
  themes; no essential status relies solely on colour.
- `pnpm test` and `pnpm build` pass, and affected frontend tests cover the new
  state handling.

## Deliberately deferred decisions

- Backend progress estimation: defer until there is a reliable definition of
  estimated/total provider work.
- URL routing: keep client-side views initially unless shareable/bookmarkable
  location state is needed.
- A component library: avoid one unless the CSS-token implementation reveals a
  concrete accessibility or maintenance gap.
- Brand colour choice: decide from the initial themed prototype; it is a visual
  preference rather than a product dependency.
