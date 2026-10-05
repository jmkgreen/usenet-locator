# NNTP indexing and coverage semantics

## 1. Core model

NNTP article numbers are scoped to a server/endpoint and newsgroup. They are not global article identifiers and SHALL NOT be assumed to correspond across providers.

Message-ID is the canonical cross-provider identity used for article deduplication.

The system SHALL therefore model at least:

- provider/account;
- endpoint;
- newsgroup;
- canonical article (Message-ID);
- group membership/crosspost relationship;
- endpoint/group article location (including endpoint-local article number when known);
- scan job and durable checkpoint;
- endpoint/group coverage history.

## 2. Historical date-range discovery

Users request whole-day inclusive date ranges rather than article-number ranges.

The indexer SHALL discover an appropriate historical article-number window on the selected endpoint. The exact algorithm is left to the implementation agent, but it must tolerate:

- sparse or missing article numbers;
- dates that are not perfectly monotonic with article numbers;
- provider-specific retention/availability;
- malformed/unusual headers without allowing one article to abort a large scan.

A configurable date margin SHALL allow scanning beyond the requested boundary. Only coverage actually examined should be recorded.

## 3. Batching and commits

Batch size should be selected automatically with an optional override.

Checkpoint semantics SHALL be explicit in the technical design. A checkpoint must correspond to durable database state. On pause/failure/restart, the system may safely repeat some work, but SHALL not silently skip uncommitted work. Repeated work must remain idempotent through Message-ID deduplication and appropriate uniqueness constraints.

## 4. Provider use

All enabled endpoints participate in normal fan-out scans. A disabled endpoint keeps its historical evidence but receives no new work and does not block aggregate completion.

Transient errors should be retried according to a bounded policy. Persistent endpoint errors should become visible and should not necessarily stop unrelated work on healthy endpoints.

Account-level connection and usage limits SHALL be respected even when multiple endpoints share the same account.

## 5. Coverage semantics

Coverage is endpoint + newsgroup specific.

The UI/API SHALL avoid statements such as “complete” when only one endpoint has been scanned. A timeline unit may be aggregate-`complete` only when every enabled endpoint has successful durable evidence for its exact UTC interval. `pending` means one or more enabled endpoints have outstanding work; `gaps` means one or more have no successful evidence. Endpoint-specific states/reasons remain visible and this never claims global Usenet completeness.

## 6. Supplementary scanning (later stage)

Later functionality SHALL support targeted and fuller supplementary scans. A supplementary endpoint can discover canonical articles absent from the primary endpoint. Its local article numbers are independent.

When the same Message-ID is discovered at multiple endpoints, retain one canonical article and multiple endpoint/group locations/availability records.

Missing numbers on endpoint A SHALL NOT be translated directly into article-number requests on endpoint B.

## 7. On-demand body retrieval

Opening/requesting an article triggers body retrieval if decoded text is not already cached.

The retrieval layer should be able to use known endpoint availability and configured provider priority/health without changing the canonical article identity. Failures should distinguish, where possible, between provider/network failure and article unavailability.
