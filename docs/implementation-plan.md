# Implementation plan

## Stage 0 — technical design

Before main coding, propose and obtain approval for:

- backend stack and NNTP client strategy;
- PostgreSQL schema/migrations/indexes;
- historical date-to-article-range discovery;
- batch/checkpoint transaction semantics;
- concurrency/resource-control model;
- REST/OpenAPI structure;
- SPA structure;
- Docker/reference and shared-PostgreSQL deployment;
- TLS and VPN fail-closed network topology;
- logging/metrics design;
- test strategy and >=80% coverage enforcement.

Identify genuine requirement conflicts rather than silently resolving them by dropping functionality.

## Stage 1 — first usable research release

Deliver:

- multiple configurable NNTP providers/endpoints/accounts;
- historical header indexing fanned out to enabled providers, with
  endpoint-specific coverage and merged canonical results;
- PostgreSQL persistence and endpoint-specific coverage;
- durable jobs/checkpoints and manual pause/resume;
- REST/OpenAPI;
- SPA with basic job controls, provider/resource status, header search, article reader;
- persistent individual and batch unwanted marks, with normal-result filtering
  and suppression of new body retrieval;
- on-demand text retrieval/cache and `.txt` export;
- TLS-by-default and optional fail-closed VPN deployment;
- operational logging/metrics and resource-pressure handling;
- reference PostgreSQL deployment plus external/shared PostgreSQL support;
- documentation and meaningful automated tests meeting the coverage gate.

Demonstrate acceptance criteria and await approval.

## Stage 2 — research/provider expansion and MCP

After Stage 1 approval, refine based on actual use and implement advanced supplementary-provider scanning/coverage where appropriate.

Add retained-history discovery and oldest-first browsing:

- an explicitly invoked, quota-accounted probe per enabled provider that uses
  group bounds and bounded overview windows to identify the earliest observed
  retained article;
- persistence of each provider/group retention observation, including the
  observed bounds, article number, parsed date, probe time, and safe outcome;
- canonical storage of the discovered article and its endpoint-local location,
  allowing a group page to show and open the oldest known article across all
  providers;
- bounded, paginated retrieval of the next oldest stored headers, merging
  providers by Message-ID while retaining their individual locations;
- individual body retrieval remains explicit. Any future bulk body export must
  require an operator-selected count and transfer-size budget, rather than
  turning a header-navigation action into unbounded provider traffic.

Add a direct earliest-header discovery workflow for any requested newsgroup:

- provide a group entry field in the Coverage view so the operator can request
  discovery for a group that has never been indexed as well as one with prior
  scans;
- make the action invoke the existing bounded retention probe against every
  enabled provider, rather than requiring the group to appear first in the
  locally stored-groups table or requiring a date-range indexing job;
- create the local newsgroup record and persist each provider's result even
  when no usable header is found, so unavailable groups, empty results,
  provider failures, and successful discoveries are distinguishable;
- show per-provider progress/outcome, group low/high bounds, earliest observed
  article/date when available, and the probe timestamp; never describe a
  bounded result as proof that no older Usenet article exists;
- after discovery, offer an explicit bounded action to retrieve the next oldest
  headers from each provider, reusing durable cursors and transfer accounting;
- keep ordinary chronological browsing read-only against locally stored
  headers, and make its boundary explain whether more history is unprobed,
  unavailable, or has exhausted the current provider cursor;
- expose the workflow through a documented REST operation that accepts only a
  validated newsgroup name and returns a job/result representation suitable
  for a new or existing group; and
- test new-group and previously scanned-group flows, provider partial failure,
  empty/undated overview rows, repeated requests, quota exhaustion, durable
  cursor continuation, and the guarantee that read-only browsing does not
  contact NNTP.

Represent discovery work visibly as a durable Coverage job queue:

- queue earliest-header discovery instead of making the Coverage action appear
  synchronous or silently running in the background;
- reuse the existing job lifecycle where practical, or add an explicit
  retention-discovery job type, with queued, running, completed, failed,
  cancelled, and interrupted states plus timestamps and safe error summaries;
- show the queue in the Coverage panel immediately after submission, including
  the requested group, operation, overall state, created/started/finished
  times, and actions such as cancel or retry where supported;
- show child/provider work beneath each discovery job, including provider
  state, group-boundary/probe information, headers discovered/stored, quota
  usage, and the reason for any partial or failed result;
- poll the queue and selected job status at the established interval, retain
  completed and failed entries long enough for the operator to understand what
  happened, and refresh retention observations and stored headers when work
  completes;
- make submission return a job identifier and render an explicit confirmation
  such as “Queued” or “Running”, so clicking the action cannot leave the user
  unsure whether provider traffic occurred; and
- add acceptance tests proving that a new-group request, an existing-group
  request, provider failures, cancellation, restart/interruption, and
  completion are all reflected in the on-screen queue.

Add a provider-agnostic chronological header browser:

- a group-scoped API that returns canonical stored headers in ascending or
  descending article-date order, using an opaque cursor based on
  `(article_date, article_id)` so pagination remains stable when providers
  contribute duplicate Message-IDs or new headers are stored;
- “older” and “newer” controls in the group view. They operate only on merged
  stored headers and do not reveal or require a provider selection;
- a clearly visible coverage/retention boundary. Reaching the oldest or newest
  currently stored page must say whether it is an exhausted provider cursor,
  an unprobed range, or simply an unindexed range—not imply that Usenet has no
  additional articles;
- an explicit, bounded “retrieve more older headers” control at that boundary,
  reusing the retained-history cursor and then refreshing the merged page;
- date-less headers sort after dated records in oldest-first browsing, with a
  visible marker rather than an invented chronology; and
- tests for cross-provider Message-ID deduplication, ordering ties, cursor
  stability, empty/partial coverage, and the guarantee that ordinary older or
  newer navigation makes no NNTP request.

Add the group timeline browser:

- drill down from known years to months and days, with a UTC date jump for an
  unseen period;
- calculate aggregate completion only from durable successful evidence for all
  enabled endpoints, while retaining visible endpoint-level gap/pending states;
- provide gap-only completion and read-only period navigation; and
- share page-size preference and total-record reporting with every article list.

Implement MCP after REST/web are established. Reuse existing application services. Define read vs state-changing MCP operations and access controls before exposing them.

## Stage 3 — binary/NZB functionality

After the application has been used for historical research, implement:

- optional multipart grouping;
- missing-segment reporting;
- NZB generation;
- SABnzbd HTTP API submission;
- associated tests and UI.

## Stage 4 — archive quality and interoperability (proposed)

Subject to approval of [`archive-quality-proposal.md`](archive-quality-proposal.md),
add versioned normalization and quality flags, verification reports, safe
article rendering, standard import/export, and high-confidence thread
navigation. Preserve source values and provenance; make cleanup reversible and
do not silently delete malformed, unwanted, or duplicate content.

## Stage 5 — provider deletion observability (proposed)

Subject to approval of
[`provider-deletion-observability-proposal.md`](provider-deletion-observability-proposal.md),
record endpoint-specific availability history, distinguish confirmed provider
deletion from transient failure, and provide a chronological/searchable
Deleted content workspace. Provider observations must never delete canonical
articles, locations, cached bodies, or coverage evidence.

## Stage 7 — optional enhancements

Potential future work includes a dedicated CLI, quote-based thread inference,
log-search API/UI, optional full-text body search, and other features justified
by real usage.

These are not Stage 1 acceptance requirements.

## Stage 6 — web console modernization (proposed)

Subject to approval of [`ui-modernization-proposal.md`](ui-modernization-proposal.md),
modernize the SPA in three safe increments: application shell and theme
foundation; the Index workspace with truthful per-provider progress; then
focused Search, Coverage, and Watchlist workflows. Preserve all existing API
and safety semantics. Do not display percentage-complete provider progress
until the backend can supply a reliable documented total or estimate.
