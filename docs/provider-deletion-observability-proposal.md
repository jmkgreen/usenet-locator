# Provider deletion observability proposal

## Status

Proposed planning material. This document defines how provider-side article
unavailability could become visible without deleting local records. It is a
future capability and does not authorize local deletion.

## Purpose

An article that was previously observed on an endpoint may later become
unavailable there. That change is useful operational evidence: it helps a user
understand retention changes, provider differences, and which portions of a
local archive are no longer retrievable from a particular endpoint.

The application should distinguish provider observations from conclusions about
why content disappeared. A failed request is not automatically proof of
deletion.

## Core invariants

- Never delete the canonical article because a provider no longer serves it.
- Never delete its group memberships, endpoint locations, cached body, or
  coverage evidence solely because retrieval failed.
- Availability is endpoint- and group-specific; one provider's result must not
  be generalized to all providers.
- A transient network, authentication, quota, or server error must not be
  labelled as deletion.
- A confirmed deletion observation must retain the time, endpoint, group,
  article identity, request type, and safe protocol outcome.
- Provider observations are evidence, not an accusation about provider policy.

## Detection model

Record an availability observation when a known endpoint location is checked,
for example during explicit body retrieval, a bounded repair probe, or a
targeted operator request.

Candidate states are:

- `available` — the endpoint successfully returned the requested article or
  header;
- `deleted` — the endpoint gave a stable, article-specific not-found or
  retention response after the request was valid;
- `temporarily_unavailable` — timeout, connection failure, server failure,
  throttling, quota, or other retryable condition;
- `not_authorized` — credentials or permissions prevented the check;
- `unsupported` — the endpoint does not support the requested operation; and
- `unknown` — the result was insufficient to classify safely.

The exact NNTP response code and a redacted diagnostic may be retained for
diagnosis, but credentials, raw authentication exchanges, and provider secrets
must never enter the observation record or logs.

Deletion classification should require a provider-specific valid response and,
where practical, a repeat confirmation. A failed body fetch alone should not
change an article to `deleted` if the endpoint location has not been checked
with an appropriate header or article command.

## Data model direction

Add an endpoint-location availability history rather than a single mutable
boolean. A conceptual observation contains:

- canonical article ID;
- endpoint ID;
- newsgroup ID;
- endpoint-local article number when known;
- observation state;
- request/probe type;
- safe response classification and optional protocol code;
- observed timestamp;
- normalizer/schema version if parsing was involved; and
- a link to the triggering job or explicit operator request where applicable.

The current location record may expose its latest known state as a convenient
summary, but the history is required for chronology and auditability. Cached
text remains locally readable even when every known provider location is
marked deleted.

## Deleted content workspace

Add a dedicated **Deleted content** area under Coverage or Search. It should
support:

- chronological browsing by observation date, article date, and group;
- search by subject, author, Message-ID, group, and provider;
- filters for provider, endpoint, deletion state, and confirmation status;
- article detail showing the local header, cached-body state, known locations,
  and the exact provider observations;
- comparison of the same canonical article across providers;
- links back to the ordinary article and coverage views; and
- an explicit distinction between locally cached content and content that is
  no longer retrievable from a selected provider.

The workspace must not imply that the article was deleted globally. Suggested
wording is “Observed unavailable on Provider A at [time]” rather than “The
article was deleted.”

## Provider and content reporting

Once sufficient observations exist, provide aggregate reports such as:

- deletion/unavailability observations by provider and endpoint;
- observations by newsgroup and article-date range;
- provider comparison for the same canonical articles;
- percentage of checked locations that became unavailable; and
- content characteristics of affected records, such as group, date, MIME
  category, size band, or repeated subject pattern.

These are observational statistics, not proof of provider intent. Reports must
show sample size, check coverage, time window, and the distinction between
confirmed deletion and other unavailable states. Small samples should be
labelled inconclusive.

## User controls and safety

- Checking a location must be explicit or part of a documented bounded job.
- Every provider-affecting operation remains subject to account connection and
  transfer limits.
- A user may request a bounded recheck of selected deleted observations.
- Rechecks can change a record back to `available` if the provider serves it
  again; history must remain intact.
- The UI must not offer a “delete locally because provider deleted it” action.
- Local deletion, when eventually implemented, remains governed by the
  requirements' separate deferred deletion policy.

## Suggested delivery order

1. Persist endpoint-location availability observations and classify failures.
2. Add explicit single-article and bounded batch rechecks.
3. Add article-detail provider comparison and the Deleted content workspace.
4. Add chronology and search filters.
5. Add aggregate provider comparison with sample-size and confidence context.

## Acceptance themes

- A provider failure cannot be misclassified as deletion.
- Confirmed unavailability never removes local canonical data or cached text.
- The same article can be available on one endpoint and deleted on another.
- Availability history is append-only apart from retention/maintenance rules.
- Deleted-content searches use the same bounded pagination rules as ordinary
  article lists.
- Reports expose uncertainty, sample size, and observation coverage.
