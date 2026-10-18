# Features implemented

This catalogue describes the capabilities in the current source tree,
including uncommitted changes if present; it is not a claim about any
particular published image or that a delivery stage has been accepted. For a
product-level introduction, see the [overview](../README.md); for future work,
see [features planned](features-planned.md). The [user guide](user-guide.md)
explains the workflows and the [deployment guide](deployment.md) their setup.

## Indexing and job control

- Queue a historical header scan for a group and inclusive date range, with a
  configurable date margin. Work fans out to enabled providers and stores
  endpoint-specific evidence.
- View recent indexing and discovery jobs, per-provider states, counts,
  transfer usage, and errors in Coverage. Look up an older job by ID; pause,
  resume, or cancel where its state permits. Interrupted work requires manual
  resume after a restart or failure.
- Start an operator-requested supplementary scan using a prior job as a source,
  with an optional transfer budget. This is not automatic cross-provider hole
  detection; see [planned supplementary work](features-planned.md#provider-research-and-automation).

## Search and read

- Search **stored headers** by subject, author, exact Message-ID, newsgroup,
  and inclusive UTC dates, with paginated results. Mark individual or selected
  articles unwanted, and include them explicitly when needed.
- Inspect stored article metadata and known newsgroup memberships. Explicitly
  retrieve text, reuse the cached text, and download it as `.txt`. The UI does
  not automatically fetch bodies during header browsing.
- New overview records retain their original field bytes and provider location,
  versioned normalized values, and informational quality flags. The reader
  can reprocess retained source locally. Existing articles without retained
  source remain searchable but cannot be reprocessed from source.
- Explicit body retrieval records bounded raw headers and body bytes, extracts
  text and MIME-part metadata, and presents sanitized HTML without active
  content or external resources. Existing text-only caches remain readable.
- Coverage includes paginated, read-only archive verification checks. Findings
  are possible issues to review, not automatic cleanup or deletion decisions.
  Overviews with invalid Message-IDs are retained separately with their
  endpoint, group, and original fields for inspection; they do not become
  canonical articles. Provider article-number identity changes are recorded.
- This is a text research workflow; attachment access and binary/NZB workflows
  should not be assumed to be available. See [planned reading and archive
  work](features-planned.md#archive-quality-reading-and-interoperability).

## Providers and coverage

- Configure accounts and endpoints, including enabled status, priority,
  connection limits, and optional transfer limits. The provider view shows
  safe status and quota information; configuration and secrets are file-based,
  not editable in the UI.
- Explicitly run a provider compatibility check and inspect successful
  qualification history. Checks contact the provider and consume quota.
- Browse stored groups and endpoint-specific coverage, including a year/month/day
  timeline. Open local articles in a selected period or request completion of
  outstanding coverage. A result from one endpoint does not establish coverage
  at another.

## Retained history and watchlist

- Queue earliest-header discovery for a new or previously scanned group across
  enabled providers. Results show individual outcomes, bounds, and observed
  headers when found. The probe examines at most the first 1,000 article numbers
  from each provider's reported low bound: it is not a global oldest-date or
  complete-history guarantee.
- Explicitly fetch a bounded next batch of oldest headers and browse merged
  stored headers chronologically. Ordinary browsing reads the local database;
  provider requests occur only when explicitly requested or scheduled.
- Schedule recurring bounded retention checks for selected groups with the
  watchlist while the service is running.

## Deployment and interfaces

- A web console and JSON REST API are provided; see the [API definition](../api/openapi.yaml).
- Deploy with the reference Docker Compose stack or an external/shared
  PostgreSQL instance. NNTP uses TLS unless plaintext is explicitly acknowledged;
  credentials are supplied as secret files. Optional VPN routing depends on a
  separately managed gateway and tested kill switch, not an in-app VPN service.
- The reference configuration binds to loopback. Use an authenticated HTTPS
  reverse proxy before exposing the application beyond the host; see
  [deployment security](deployment.md#secrets-and-tls).
