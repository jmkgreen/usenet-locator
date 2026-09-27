# Product requirements

## 1. Purpose

Build a self-hosted application that indexes headers from selected Usenet newsgroups over user-specified historical date ranges and stores them locally for research. The primary use case is locating and reading historical text discussions without repeatedly searching remote NNTP servers.

The system SHALL provide local header search, on-demand text retrieval, persistent indexing jobs, and accurate endpoint-specific coverage information. It SHALL be suitable for a consumer/homelab server that is normally performing other work.

The first usable release is primarily a **historical header indexer and text research tool**. Multipart binary grouping/NZB support and advanced supplementary-provider scanning are confirmed later requirements, not Stage 1 prerequisites.

## 2. Target environment and priorities

- Target use is personal/consumer homelab use, not commercial or multi-tenant operation.
- Server-side memory efficiency is highly desirable and SHALL be considered when selecting architecture, libraries, concurrency models, caches, and batch sizes.
- The backend SHOULD ordinarily operate below 512 MiB RAM and SHALL be designed to remain below 1 GiB RAM under a conservatively configured workload. The technical design must state the assumptions and bounded controls behind this target; unusually large user-selected date ranges must not cause unbounded memory growth.
- Correctness and reliability SHALL take precedence over indexing speed.
- Human source-code readability SHALL NOT be used as a weighted factor when choosing the implementation language. Code is expected primarily to be inspected and maintained with LLM/automated assistance.
- The implementation SHALL avoid unnecessary enterprise complexity.

## 3. Application architecture

- Backend language/framework: no user preference; implementation agent selects and justifies it.
- Frontend: modern single-page application using React, Vue, or comparable technology.
- API: JSON REST API documented with OpenAPI.
- Database: PostgreSQL from the outset.
- A reference deployment SHALL provide an application container plus a PostgreSQL container.
- The application SHALL also support an externally managed/shared PostgreSQL instance; PostgreSQL SHALL not be assumed to live in the application's Docker stack.
- Primary target host: TrueNAS SCALE using Docker.
- A command-line client is useful but lower priority than the web UI.
- MCP support SHALL be designed as a later interface over existing application services, after the REST API and web UI are working.

## 4. NNTP configuration

The design SHALL distinguish accounts, endpoints, and indexing sources. Multiple endpoints may share credentials, connection limits, or usage quotas.

Initial known endpoints are:

| Provider/account | Endpoint | Existing SABnzbd priority (reference only) |
|---|---|---:|
| Newshosting | `news.newshosting.com` | 1 |
| Newshosting | `news-nl.newshosting.com` | 2 |
| NewsgroupDirect | `eu-tst.newsgroupdirect.com` | 5 |
| NewsgroupDirect | `news.newsgroupdirect.com` | 5 |
| Tweaknews | `newshosting.tweaknews.eu` | 5 |
| Easynews | `secure-eu.news.easynews.com` | 10 |

These priorities are reference information and do not require duplication of SABnzbd's selection algorithm.

Requirements:

- Configure a primary indexing endpoint.
- Support an ordered endpoint preference for scanning and later retrieval. The initial ordering may reflect expected article completeness and provider knowledge; it need not duplicate SABnzbd's selection algorithm.
- Configure connection limits per account independently of SABnzbd.
- Support provider-required credentials.
- Support configurable monthly or total transfer allowances for metered accounts and suspend use when a configured limit is reached.
- Retry transient endpoint failures sensibly; if an endpoint must be paused, unrelated work that can safely continue MAY continue.
- TLS requirements and VPN routing are defined in `security-privacy.md`.

## 5. Historical indexing jobs

### 5.1 Job definition

- One newsgroup per job.
- Inclusive whole-day start and end dates.
- Configurable date margin outside the requested interval to cope with imperfect chronological ordering.
- Automatically discover an approximate article-number range for the requested historical dates on each relevant endpoint.
- Never assume NNTP article numbers correspond between providers or endpoints.
- Automatically skip ranges already successfully scanned for the same endpoint/newsgroup.
- Record coverage per endpoint and newsgroup. A scan of one provider SHALL NOT be represented as proof of completeness on another provider/backbone.

### 5.2 Execution and progress

- Automatically select a batch size, with optional user override.
- Support multiple jobs subject to configurable resource and connection limits.
- Report at least: headers retrieved, articles stored, current/approximate article date, throughput, errors, and ETA where an ETA can be estimated responsibly.
- Persist checkpoints and progress durably.
- Manual pause SHALL be available, including to free resources/connections for SABnzbd.
- After application restart or relevant connectivity failure, interrupted work SHALL require manual resume rather than silently restarting.
- Pause/resume SHALL not lose committed progress.
- Partial batches SHALL not cause articles to be silently skipped or duplicated.

### 5.3 Supplementary providers — later capability

Advanced supplementary scanning MAY follow Stage 1, but the data model SHALL not make it impractical.

Later behaviour SHALL include:

- consult supplementary providers when the primary encounters missing article numbers or failed header batches;
- allow targeted or fuller supplementary scans;
- record endpoint-specific scan history, availability, and coverage statistics;
- deduplicate articles globally by Message-ID while retaining endpoint-specific article locations;
- optionally test whether an article found on one provider is retrievable from another.

The endpoint preference order is an input to this later supplementary-hole checking. Stage 1 SHALL retain the endpoint-specific state necessary to support it, but does not need to automatically scan other providers for every hole.

## 6. Header and article storage

PostgreSQL SHALL store at least these common header fields when supplied: Message-ID, subject, author/from, date, references, bytes, and lines, plus relational and operational metadata required by this specification.

- Message-ID is the canonical global article identity for deduplication.
- Crossposted articles SHALL be stored once and linked to each known newsgroup membership.
- Article detail, including a header-only view before body retrieval, SHALL show every known newsgroup membership for that canonical article.
- Endpoint-specific article-number/location data SHALL be stored separately from the canonical article.
- Headers are retained indefinitely until explicitly deleted.
- No fixed database-size ceiling is required, but storage growth SHALL be visible to the user.

## 7. Text article retrieval

- Article bodies SHALL be fetched only when explicitly opened or requested.
- Cache the decoded text body only; retaining the complete original article is not required.
- Cached bodies SHALL NOT be full-text indexed in the initial requirements; header search is sufficient.
- Plain-text display SHALL preserve line breaks and formatting.
- For MIME/multipart articles, display readable text and provide access to individual attachments.
- Because the original complete article is not retained, the UI/documentation SHALL not imply that an attachment remains locally available unless it actually has been separately retained. A later attachment request may require NNTP retrieval again.
- Export decoded article text as `.txt`.

## 8. Search and web UI

Initial web UI SHALL include:

- header search and article reading;
- a storage/coverage browser that starts with a list of stored newsgroups and their article counts, then permits drill-down into date ranges and individual articles;
- basic indexing-job creation/status/pause/resume controls;
- conventional paginated search results with sorting and filtering;
- polling of the REST API every few seconds for indexing progress rather than requiring WebSockets/SSE;
- a simple plain-text article reader;
- a provider-status view showing configured NNTP providers/endpoints and lightweight operational statistics.

Essential search filters:

- subject;
- author;
- Message-ID;
- newsgroup;
- date range.

Individual articles are the default result representation.

## 9. Binary grouping and NZB — later enhancement

When implemented:

- optionally group multipart binaries rather than replacing the individual-article view;
- group using subject-pattern matching, segment numbering, posting dates, and other useful header metadata;
- retain constituent articles and Message-IDs;
- show incomplete groups and identify missing segments;
- generate NZB files;
- submit generated NZBs directly to SABnzbd through its HTTP API;
- clearly identify NZBs/groups that are incomplete.

## 10. Resource efficiency and pressure handling

The application SHALL expose configurable application-level controls such as worker/concurrency/connection limits in addition to any Docker/host hard resource limits.

It SHALL expose lightweight resource-utilisation information so a user can recognise an undersized configuration rather than merely observing repeated crashes.

When resource pressure becomes material, intended behaviour is:

1. warn and surface the condition;
2. throttle/reduce application work where practical;
3. if safe operation cannot continue, fail/crash rather than silently corrupt state.

The condition and relevant transition SHALL always be logged. Hard container limits remain the final enforcement boundary.

The reference deployment SHALL use conservative defaults appropriate to the memory target above. Actual usable limits depend on the selected stack and host; all worker, batch, queue, cache, and database-pool limits must be configurable.

PostgreSQL may be shared with other applications. The application SHALL use PostgreSQL conservatively (including a bounded connection pool) and SHALL not attempt to administer an externally managed PostgreSQL server.

## 11. Logging, privacy, metrics, security

The requirements in [`security-privacy.md`](security-privacy.md) are normative and include:

- no end-user search/activity history;
- data-minimising operational logging;
- configurable normal/debug logging depth;
- stdout/stderr container logging with optional persistent file logging;
- lightweight operational metrics only;
- TLS-required NNTP by default;
- explicit opt-in required for plaintext NNTP;
- optional whole-application external traffic routing through one VPN container;
- fail closed when VPN routing is configured and the VPN is unavailable.

## 12. Documentation

Delivery SHALL include:

- repository README and documentation index;
- deployment instructions for TrueNAS SCALE/Docker;
- configuration and secrets reference;
- instructions for using bundled PostgreSQL and an existing/shared PostgreSQL instance;
- user guide;
- OpenAPI documentation/examples;
- architecture documentation;
- database/data-model documentation;
- NNTP indexing/coverage documentation;
- security/privacy/logging documentation;
- automated-test and acceptance-test instructions;
- troubleshooting and developer guidance.

## 13. Delivery process

Implementation SHALL proceed stage by stage. Each stage SHALL include meaningful automated tests and a review/approval point before the next stage begins. See `implementation-plan.md` and `testing-acceptance.md`.

## 14. Deferred deletion policy

Deletion is a confirmed later requirement but is not a Stage 1 acceptance requirement. Its planned scopes are: an individual canonical article, a date range within a selected newsgroup, and an entire newsgroup. The design must preserve enough ownership/coverage semantics to make these operations well-defined, including the treatment of crossposted canonical articles, cached bodies, endpoint locations, and coverage records. Exact user-interface workflow and retention consequences will be reviewed before implementation of deletion.
