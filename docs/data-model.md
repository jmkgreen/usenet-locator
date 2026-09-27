# Data model requirements

This document defines semantics, not mandatory SQL table names.

## 1. Canonical article

One canonical record per Message-ID. Store the common header fields required by the product:

- Message-ID (unique canonical identity);
- subject;
- author/from;
- article date;
- references;
- bytes when supplied;
- lines when supplied;
- other minimal metadata needed for parsing/state/versioning.

Do not store duplicate canonical articles simply because the same Message-ID appears on multiple endpoints or in multiple groups.

## 2. Newsgroups and crossposts

Represent newsgroups independently and use a relationship between canonical articles and newsgroups. A crossposted article is one article with multiple memberships.

## 3. Providers, accounts and endpoints

Keep these concepts distinct:

- **account/provider account**: credentials, shared connection/quota constraints;
- **endpoint**: NNTP host/port/TLS configuration and priority/role;
- **indexing source/location**: endpoint + newsgroup + endpoint-local article number/availability.

The schema must allow multiple endpoints to share an account.

## 4. Endpoint-specific article locations

For each known occurrence, retain enough information to request the article from that endpoint/group later. Article numbers are endpoint/group scoped and must never be treated as global.

## 5. Scan jobs and checkpoints

Persist:

- requested group and date range;
- configured date margin/effective range where useful;
- endpoint(s) involved;
- lifecycle state;
- durable checkpoint/progress;
- progress counters and error state needed to resume/diagnose;
- creation/update/completion timestamps necessary for operations.

Do not turn job metadata into an end-user behavioural audit trail.

## 6. Coverage

Persist endpoint/newsgroup scan coverage sufficiently to:

- skip successfully scanned ranges;
- identify gaps/failures;
- show what has actually been examined;
- support later supplementary scans without falsely treating primary-provider coverage as global coverage.

The technical design must define how overlapping/repeated ranges are represented efficiently.

## 7. Cached bodies and attachments

Cache decoded text body on demand. Full raw articles need not be retained.

Attachment metadata may be retained as necessary to present/access MIME attachments, but the model/UI must distinguish between an attachment that is actually stored locally and one that requires a future NNTP fetch.

## 8. Future binary grouping

The Stage 1 schema should not prevent later modelling of a logical multipart binary group containing individual canonical articles/segments, including missing-segment information and generated NZB metadata. It is not necessary to fully implement that model in Stage 1 if doing so would add needless complexity.

## 9. Indexing and performance

The implementation agent SHALL propose database indexes for the required search filters and uniqueness/integrity constraints. PostgreSQL connection use must be bounded and appropriate for a potentially shared homelab PostgreSQL instance.
