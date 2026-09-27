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
- primary-provider historical header indexing;
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
