# Stage 0 technical proposal

**Status:** draft for approval. This document defines the proposed Stage 1
implementation. It does not begin Stage 1 delivery.

## 1. Proposed stack

| Area | Proposal | Reason |
|---|---|---|
| Backend | Go 1.26+, `net/http`, Chi router | Small, static container image; inexpensive goroutines; context-aware I/O; straightforward bounded concurrency. |
| NNTP | A narrow internal `NNTPClient` adapter; a third-party implementation may sit behind it only after qualification | This keeps provider compatibility, TLS, cancellation, streaming limits, and retries under one testable contract without prematurely committing to a bespoke wire implementation. |
| Database | PostgreSQL 16+, `pgx`/`pgxpool`, SQL migrations embedded in the application | Mature PostgreSQL driver with a bounded pool, transactional batch writes, and no ORM hidden query behaviour. |
| API | REST/JSON under `/api/v1`; OpenAPI 3.1 maintained as source and validated/generated in CI | A stable interface for the SPA, later CLI, and later MCP service layer. |
| Frontend | React, TypeScript, Vite, TanStack Query and Router | A conventional compact SPA with polling, typed API clients, and a static build served by the application container. |
| Observability | Structured `slog` to stdout/stderr; Prometheus-format `/metrics`; optional rotating file sink | Low overhead and useful in Docker/TrueNAS without a required monitoring stack. |
| Testing | Go standard test tooling plus Testcontainers PostgreSQL integration tests; Vitest/Testing Library and Playwright smoke tests | Fast deterministic unit tests, real PostgreSQL validation where it matters, and essential UI-path coverage. |

The application container serves the compiled SPA and API. It does not embed
PostgreSQL. The reference Compose deployment adds PostgreSQL; an external
database is selected purely by configuration.

### NNTP dependency review and adoption gate

The application will **not** decide in advance to write an NNTP wire client
from scratch. It will define and own only a small adapter interface covering
connection setup, authentication, capability/reader-mode negotiation, group
selection, overview/header reads, streaming article/body reads, and orderly
close. Job and retrieval services depend on that interface rather than a
specific library.

Two current Go candidates were assessed on 27 September 2026:

| Candidate | Useful coverage | Assessment |
|---|---|---|
| `github.com/go-newsgroups/nntp` | Implicit TLS, `AUTHINFO`, `CAPABILITIES`, `MODE READER`, `GROUP`, `OVER` with `XOVER` fallback, article retrieval, and substantial scripted protocol tests | Promising and actively changing, but at pre-1.0 `v0.1.0`; it requires Go 1.26.4, has no `HEAD` API, and uses `ReadDotLines`, materialising overview and complete article bodies in memory. It cannot directly meet the configured streaming body-size limit. |
| `github.com/dustin/go-nntp/client` | Implicit TLS and `STARTTLS`, capabilities, `HEAD`, `BODY`, `ARTICLE`, and streaming dot readers | The most functionally complete candidate for this use, but its last upstream commit is July 2021, its client package has no tests, and its full test run currently fails in an example package on the supported development platform. It is unsuitable as an unqualified direct dependency. |

Before implementation commits to either candidate, a short NNTP compatibility
spike will run an adapter implementation against scripted transcripts and the
configured providers. It must demonstrate: strict certificate/hostname
validation; no TLS downgrade; authentication; `CAPABILITIES` and `MODE READER`
variance; `OVER`/`XOVER` fallback; sparse/missing numbers; malformed overview
dates/fields; dot-stuffing; response limits; connection/read timeouts;
cancellation; and bounded streaming body retrieval. It must also pass the
application's normal race, test, and dependency-vulnerability checks.

The expected outcome is to use a qualified dependency behind the adapter,
contributing fixes upstream where feasible. If neither passes—particularly if
their response buffering or inaccessible connection/deadline controls cannot
be corrected—the project will implement/fork only the small reader-oriented
protocol layer required by Stage 1. The same transcript and live-provider
conformance suite then becomes its regression safety net. This is a contained
investment, not a full NNTP server implementation.

### Language decision and future boundaries

Go is proposed because the core workload is a bounded set of cancellable,
network/database-heavy operations: NNTP connections, streaming protocol
parsing, batch persistence, retries, and manual pauses. Goroutines and context
cancellation express this directly; the standard library provides TLS, HTTP,
networking, and structured logging; and `pgx` permits explicit PostgreSQL pool
control. This makes the normal sub-512 MiB target practical without relying on
a large runtime or a complex asynchronous framework.

The alternatives considered were:

| Option | Relevant strengths | Decision |
|---|---|---|
| TypeScript/Node.js | One language with the SPA and excellent web/OpenAPI tooling | Viable, but bounded long-running concurrency and memory/backpressure would require more deliberate operational controls. It is the closest alternative. |
| Rust | Predictable memory use, performance, and strong memory safety | Technically excellent for isolated high-throughput or untrusted-input components, but adds delivery complexity without a demonstrated Stage 1 benefit. |
| Python | Fast development and broad ecosystem | Less suitable for predictable resource use across concurrent streaming work. |
| Java/Kotlin or .NET | Mature service, PostgreSQL, and observability tooling | Credible choices, but their runtime/container baselines are less aligned with the target memory budget. |
| Elixir/Erlang | Robust fault isolation and concurrency | More specialised than this single-process homelab service requires. |

Future success is not a reason to rewrite a working Go service. The API,
application-service, and adapter boundaries are deliberately kept separate so a
measured bottleneck can be replaced independently. Rust would be reconsidered
for a profiled high-throughput parser/indexer, a security-sensitive MIME or
attachment-processing component, a tiny distributed scanner, or a later
CPU-intensive multipart reconstruction/hashing subsystem. Such a component
would use an explicit worker contract and PostgreSQL-backed job state; it would
not require replacing the REST API, SPA, schema, or durable scheduler.

## 1.1 Repository structure

The project will be a single monorepo. Backend and frontend have independent
builds but share one versioned OpenAPI contract, Compose deployment, CI, and
documentation:

```text
usenet-locator/
  backend/          Go service, migrations, NNTP client, and backend tests
  frontend/         React SPA, generated API client, and UI tests
  api/              OpenAPI 3.1 source contract
  deploy/           Docker Compose, TrueNAS, and VPN examples
  docs/             product, design, deployment, and user documentation
  scripts/          generation, local-development, and CI helpers
```

The production application image is a multi-stage build: it compiles the React
assets and Go binary, then serves those assets from the application container.
For development, the React dev server runs independently and proxies API calls
to the backend. Any API change is reviewed in the same repository change as its
OpenAPI update, backend implementation, generated client, and UI use.

## 2. Service boundaries and execution model

The process has four bounded subsystems:

1. The HTTP/API server validates requests and exposes reads and commands. It
   never performs an indexing scan in a request goroutine.
2. A durable job dispatcher periodically claims eligible jobs using PostgreSQL
   row locking (`FOR UPDATE SKIP LOCKED`). A single-process advisory lock makes
   accidental double dispatch impossible in the Stage 1 deployment.
3. Per-job scan workers use endpoint connections obtained from an
   account-scoped semaphore. Each worker has a bounded batch buffer and writes
   only one committed batch at a time.
4. The retrieval worker fetches requested bodies with the same account limits;
   it has lower priority than an explicitly resumed index job only when the
   account connection limit would otherwise be exceeded.

Configuration supplies hard ceilings for active jobs, per-job workers,
connections per account, database pool size, batch size, buffered header bytes,
HTTP response size, and cached-body size. Defaults will be conservative: one
active scan, one scan connection per account, a 250-article batch ceiling, a
four-connection database pool, and no unbounded queues. All readers stream
protocol lines and result rows rather than materialising a requested range.

The Stage 1 service is one application replica. Scaling it horizontally is out
of scope; the database state and locks nevertheless make a duplicate process
fail safely rather than silently run duplicate scans.

### Resource-pressure policy

The process samples Go runtime memory, queue occupancy, active connections, and
database-pool waiting. A warning threshold places the scheduler in `throttled`
state (no new jobs; active jobs reduce to their configured minimum concurrency).
A critical threshold cancels work at a batch boundary, leaves the durable
checkpoint unchanged for uncommitted input, marks the job `interrupted`, and
logs/exports the transition. If the process cannot remain safe, it exits
non-zero rather than continuing with corrupt or ambiguous state. Docker/TrueNAS
memory and CPU limits are documented as the final boundary.

The design target is ordinarily below 512 MiB: one streaming NNTP connection,
at most 250 parsed headers awaiting a transaction, bounded HTTP bodies, a small
PostgreSQL pool, and no body/full-text cache in process memory. The 1 GiB limit
remains achievable under a deliberately conservative multi-job configuration;
the deployment guide will make that configuration explicit.

## 3. Database model and migrations

SQL migrations are ordered, checksummed, and executed by `usenet-locator
migrate` or on application startup only when `migrations.auto_apply=true`.
Production external-database guidance will recommend running the explicit
command under the application role. Migrations are transactional whenever
PostgreSQL supports it and recorded in `schema_migrations`; the application
will never alter server-wide PostgreSQL settings.

### Core relations

| Relation | Key fields and semantics |
|---|---|
| `provider_accounts` | credential reference, enabled state, connection cap, optional byte allowance and accounted transfer total. Secrets themselves are never stored. |
| `nntp_endpoints` | account FK, host, port, TLS mode, explicit plaintext acknowledgement, priority, primary/supplementary role, health state. |
| `newsgroups` | canonical lower-case group name. |
| `articles` | canonical Message-ID (unique), normalized search fields, raw supplied date, parsed UTC date, references, bytes, lines, parse flags, timestamps. |
| `article_newsgroups` | article/group membership; composite primary key supports crossposts. |
| `article_locations` | endpoint/group/article-number occurrence; unique `(endpoint_id, newsgroup_id, article_number)`, with article FK and availability observations. |
| `article_bodies` | one decoded-text cache per article, content, MIME summary, fetched-at, source endpoint; no raw article. |
| `article_preferences` | one local preference row per canonical article; initially an `unwanted` state, optional local note, and timestamps. It is a user decision, not provider-derived moderation data. |
| `index_jobs` | one requested group/date range and primary endpoint, effective margin, lifecycle, immutable request parameters, counters, error summary, timestamps, and manual-resume marker. |
| `job_checkpoints` | job FK, next article number, last committed article number, discovery bounds, current approximate date, and revision. Only committed state is checkpoint state. |
| `scan_coverage` | endpoint/group interval with state `complete`, `partial`, `failed`, or `unavailable`, article-number bounds, observed date bounds, margin, scan job, and reason. |
| `endpoint_events` | bounded-retention operational endpoint failures/health transitions only; no user activity data. |

`articles.message_id` is case-preserved for display and stored with a canonical
comparison key using a unique index. An invalid or absent Message-ID is not
silently merged: it is recorded as an invalid header observation and does not
create a canonical article. The job's error counters expose this outcome.

The initial indexes are: B-tree indexes on article parsed date, Message-ID,
lower-cased author, group membership joins, and endpoint location lookup;
GIN/trigram indexes for case-insensitive subject/author search; and range/GiST
indexes for coverage overlap queries. A composite `(newsgroup_id,
article_date DESC, article_id DESC)` result index supports pagination. All
searches use keyset pagination internally; the REST response exposes a stable
opaque cursor and optional sort direction, rather than loading/offsetting large
historical result sets.

Coverage intervals may overlap due to retries and margins. A canonicalization
transaction coalesces adjacent successful intervals only where endpoint, group,
scan parameters, and successful semantics match; gaps and failed intervals are
never merged away. The coverage browser reports exactly this endpoint-scoped
evidence and never describes it as global completeness.

## 4. NNTP client and historical discovery

Each connection performs greeting validation, optional authentication, `GROUP`,
and only the explicitly enabled TLS/plaintext mode. TLS uses normal hostname and
certificate verification; there is no fallback. Plaintext configuration is
rejected unless its per-endpoint acknowledgement is true.

For a historical request, the scanner obtains the endpoint-local low/high range
from `GROUP` and probes article numbers with `HEAD` (or `XOVER`/`OVER` where the
server advertises and successfully supports it). It performs a bounded binary
search for approximate lower and upper date boundaries, then expands each side
by the configured date margin and a configurable probe/number safety window.
Because dates and numbers are imperfectly ordered, a discovery result is an
approximation, not proof: scanning filters stored results to the requested
whole-day interval, while retaining all actually examined number intervals as
coverage. Sparse, missing, malformed, and unavailable articles are counted and
recorded without aborting a scan.

Stage 1 scans only the selected primary endpoint. The model retains all
endpoint/group locations and coverage needed for later supplementary scans; it
never maps an article number discovered on one endpoint to another endpoint.

Transient protocol, timeout, and 4xx/5xx server failures use bounded
exponential retry with jitter. Authentication, TLS, configuration, and repeated
protocol errors pause the affected endpoint and mark dependent jobs
`interrupted`; unrelated jobs on healthy accounts may continue. Account byte
allowance is reserved conservatively before a request and reconciled from
observed transfer bytes; reaching the cap pauses new work on all endpoints that
share that account.

## 5. Batch, checkpoint, pause, and recovery semantics

A batch is a consecutive bounded set of attempted endpoint-local article
numbers. Parsed headers may be held only until the batch commits.

Within one PostgreSQL transaction the worker upserts canonical articles by
Message-ID, upserts memberships and endpoint locations, increments job counters,
records the examined coverage fragment, and advances the checkpoint to the next
article number. The transaction commits before the worker considers the batch
complete. A crash or cancellation before commit leaves the old checkpoint, so a
manual resume repeats the whole batch. Unique constraints make repeated work
idempotent. A failure inside a batch records an explicit partial/failed coverage
fragment and advances only through work whose durable outcome is known; it never
skips an ambiguous suffix.

Pause is a command recorded durably. Workers stop claiming work, finish or roll
back their current transaction, then transition the job to `paused`. On process
startup, `running` jobs become `interrupted` and require an explicit `resume`
command. Neither startup nor endpoint recovery auto-resumes a job. Resume
validates configuration/quota, retains the original effective range, and starts
from the committed checkpoint.

## 6. REST API and OpenAPI shape

OpenAPI 3.1 is versioned at `api/openapi.yaml`; CI validates it and checks that
server route/request/response types agree with it. All API responses use a
problem-details error body (`application/problem+json`) for validation,
conflict, unavailable, and internal errors. API payloads never contain secrets.

| Resource | Main Stage 1 operations |
|---|---|
| `/api/v1/jobs` | create/list/get; `pause`, `resume`, and cancel commands; progress and checkpoint/status reads. |
| `/api/v1/search` | paginated header search by subject, author, Message-ID, group, and inclusive date range. |
| `/api/v1/articles/{id}` | header/crosspost detail, cached-body state, request body retrieval, plain-text export, and set/clear local unwanted state. |
| `/api/v1/articles/unwanted` | atomic batch set/clear for explicit selected article IDs or a server-resolved result/storage selection. |
| `/api/v1/newsgroups` and `/coverage` | stored group counts, date drill-down, and endpoint-specific coverage. |
| `/api/v1/providers` | configured safe endpoint/account status, quotas, connection use, and health. |
| `/api/v1/system/status` | resource/throttling state, active jobs, DB-pool state, and version. |
| `/metrics`, `/healthz`, `/readyz` | operational metrics and container probes; metrics may be disabled or network-restricted. |

Progress is polled every five seconds while a job page is visible, with a
backoff when the tab is hidden. Reads are designed as reusable application
services, leaving a later MCP adapter to call the same service layer rather
than route through the browser or duplicate logic.

## 7. SPA structure

The SPA has feature routes for Jobs, Search, Article, Storage/Coverage, and
Providers/System. A shared generated API client, query cache, error presenter,
and polling hook avoid business logic in view components. The article view
shows all known group memberships, clearly distinguishes cached text from a
new NNTP retrieval, preserves text in `pre-wrap` rendering, and exports decoded
text as `.txt`. Attachment metadata, when later supplied, is labelled as either
locally retained or requiring re-fetch; Stage 1 does not imply local retention.

Search and storage result views offer single and batch unwanted-mark actions,
including a selected consecutive range. Ordinary queries exclude unwanted
articles by default and expose an explicit include-unwanted filter. The mark is
an atomic local preference update keyed to canonical article ID/Message-ID; it
does not delete the headers, body cache, endpoint locations, or coverage data.
The retrieval service rejects a new body/attachment request for a marked
article until it is unmarked. Indexing may still receive its overview header
when it scans an unknown endpoint-local number, but no later body transfer is
initiated.

## 8. Docker, PostgreSQL, TLS, and VPN topology

The ordinary reference Compose file has an `app` service and a private `db`
service with persistent volumes. The app's web port binds only to the intended
LAN/reverse-proxy network. A second Compose example omits `db` and accepts an
external `DATABASE_URL` secret. Both use Docker secrets or environment-variable
references for passwords and NNTP credentials; configuration files contain only
secret reference names.

VPN mode connects the application container's external network namespace to a
user-managed VPN gateway container (for example `network_mode:
"service:vpn"`). The gateway owns the WAN route and provides a healthcheck;
the app has no direct WAN-attached network in this mode. A dedicated internal
network reaches PostgreSQL and the reverse proxy without providing a default
external route. If the VPN container loses its tunnel/kill-switch health, its
network policy blocks egress; the app detects failed health/reachability,
surfaces `vpn_unavailable`, and pauses external operations. Consequently
application intent is not the sole protection against WAN fallback.

The deployment guide will require that a supplied VPN gateway has a kill switch
and will include a verification procedure that stops the gateway and confirms
that no NNTP connection can leave the host route.

## 9. Logging, metrics, and privacy

Logs record lifecycle, safe configuration outcome, checkpoint transitions,
connection/TLS/VPN failures, quota state, and resource transitions. They redact
configured secrets and never include search terms, viewing history, article
bodies, headers, or credentials. Debug logs add safe protocol/event context but
keep the same exclusions. Optional file output uses size/time rotation and a
bounded retention count.

Metrics use only fixed, low-cardinality labels such as endpoint ID, outcome,
and job state. They include memory/CPU, queue/batch depth, active connections,
job counts, throughput, errors, quota bytes, DB-pool use, and throttling state.
Subjects, authors, Message-IDs, article content, and query values are forbidden
in labels and metric values intended as operational dimensions.

## 10. Test and coverage plan

Unit tests use deterministic NNTP transcript fixtures and a controllable clock.
Integration tests run migrations against PostgreSQL and cover transaction,
constraint, coverage-range, cursor, and recovery behaviour. The UI tests cover
job commands, polling states, search filters/results, article reader/cache
states, and provider/resource displays. A small Compose smoke test verifies the
reference deployment and OpenAPI endpoint.

Required focused tests cover discovery with sparse/non-monotonic/malformed
headers; date inclusivity/margins; Message-ID deduplication; crossposts;
endpoint-local locations; account quotas/connection caps; TLS/plaintext refusal;
batch rollback/retry; pause/restart/manual resume; search paging; body caching;
individual/batch unwanted marking and retrieval suppression;
redaction and metric-label policy; and throttle/fail-safe state transitions.

CI will enforce at least 80% line coverage for backend first-party packages and
at least 80% statement coverage for frontend first-party source. Generated
OpenAPI client code, migration embed glue, and executable entrypoints may be
excluded with documented justification; no domain, scheduler, protocol, or API
handler packages are excluded. Coverage reports and threshold failures are
published with CI results.

## 11. Decisions and approval requested

No requirement conflicts were found. The following ordinary design choices need
approval before Stage 1 begins:

- Go/React/PostgreSQL stack and the NNTP adapter/third-party qualification
  gate, with a small in-house or maintained-fork fallback only if required.
- Single-process durable dispatcher with bounded workers and manual recovery.
- Endpoint-local, approximate discovery plus actual-examined coverage semantics.
- Transactional batch checkpoint model that permits idempotent rework but never
  skips uncommitted input.
- Docker VPN namespace/gateway topology as the fail-closed mechanism.
- The stated coverage metrics and exclusions.
- Persistent local unwanted marks, including batch actions, default result
  filtering, and suppression of new body/attachment retrieval.

On approval, Stage 1 will begin with repository scaffolding, configuration
validation, schema migrations, the NNTP protocol test harness, and the CI
coverage gate.
