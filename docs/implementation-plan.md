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

## Stage 4 — optional enhancements

Potential future work includes a dedicated CLI, richer thread navigation, log-search API/UI, and other features justified by real usage.

These are not Stage 1 acceptance requirements.
