# Archive quality and interoperability proposal

## Status

Proposed planning material. This document describes candidate capabilities for
making the local archive easier to search, read, verify, import, and export. It
does not change the current Stage 1 acceptance requirements.

## Purpose

The application should remain a provider-aware historical indexer, while
gradually adding an archive-quality layer around the canonical article model.
That layer should make imperfect Usenet data understandable without silently
discarding information or turning routine research into a large offline
processing project.

The design priorities are:

- preserve source evidence and endpoint provenance;
- make normalization repeatable and explainable;
- keep cleanup reversible wherever possible;
- keep body retrieval explicit and quota-accounted;
- retain bounded memory and database work suitable for a homelab; and
- make the common reading and export workflows simple.

## Proposed capabilities

### 1. Normalization and quality metadata

Introduce a versioned normalization pipeline for headers and decoded text. It
should tolerate malformed input and record warnings rather than rejecting an
otherwise useful article.

Candidate operations include:

- character-set and transfer-encoding normalization to UTF-8;
- tolerant date, address, subject, and References parsing;
- normalized subject forms for search and thread grouping;
- MIME-part classification and safe text extraction;
- HTML/plain-text separation;
- detection of malformed, truncated, undecodable, or contradictory fields; and
- a parser/normalizer version on derived values so they can be rebuilt later.

Original supplied values must remain available where they are needed for
diagnosis. Normalized values must never be presented as provider assertions.

Each article may expose quality flags such as `malformed_header`,
`unknown_encoding`, `body_truncated`, `html_only`, or `threading_uncertain`.
Flags are informational by default and must not hide an article from ordinary
search.

### 2. Reversible cleanup and verification

Add dry-run maintenance jobs and reports before adding destructive cleanup.
They should identify:

- duplicate or conflicting canonical identities;
- repeated content with different Message-IDs;
- group-membership inconsistencies;
- orphaned endpoint locations;
- cached bodies without valid canonical articles;
- coverage records that do not match stored locations;
- invalid or suspicious dates;
- articles whose body cannot be decoded safely; and
- interrupted or inconsistent indexing counters.

The first cleanup actions should be reversible classifications or local
visibility changes. Existing unwanted marks remain distinct from parser
warnings, provider availability, and deletion observations.

Any later deletion operation must use the deferred deletion policy in the
requirements and must show its exact scope before execution.

### 3. Spam and low-value content controls

Provide a transparent classification layer rather than an opaque automatic
filter. Initial rules may use sender, subject, headers, group, repetition, and
user feedback. A classification should include:

- label and confidence;
- rule or model version;
- observed evidence;
- whether it affects search visibility; and
- an undo or override path.

The default should be to retain the article and allow explicit inclusion in
search. Provider deletion and local unwanted decisions must never be conflated
with spam classification.

### 4. Thread and conversation reconstruction

Build thread navigation in increasing levels of confidence:

1. `References` and `In-Reply-To` relationships;
2. normalized subject and author/date heuristics;
3. quote-prefix and quoted-text matching for missing relationships.

Store relationship provenance and confidence. The UI should show parent,
reply, related discussion, and orphan states, while clearly labelling inferred
links. A chronological view remains the fallback and must not depend on a
complete thread graph.

### 5. Safe article rendering

The reader should offer a clean reading view and an evidence view:

- readable plain text with preserved line breaks;
- sanitized HTML when present, with remote resources blocked by default;
- folded quoted text and signatures;
- MIME-part and attachment metadata;
- raw header inspection;
- encoding and parsing warnings; and
- copy/export actions for the displayed representation.

Rendering must not make an NNTP request implicitly. External images, scripts,
iframes, and tracking resources must not execute in the browser.

### 6. Standard import and export

Prioritize interoperable formats over a custom archive format.

Initial export candidates:

- one article as `.eml`;
- decoded text as `.txt`;
- selected headers as JSON or CSV;
- selected messages as mbox or maildir;
- a thread bundle containing messages and relationship metadata; and
- a portable manifest containing source, coverage, and normalization metadata.

Initial import candidates:

- mbox;
- maildir;
- individual `.eml` files; and
- a `usenet-locator` export bundle.

Imported messages must retain source provenance and must pass through the same
canonical Message-ID, cross-post, normalization, and quality rules as NNTP
data. Import must not imply that a message is currently available from any
provider.

### 7. Storage and indexing improvements

If body volume becomes material, evaluate individually compressed,
content-addressed decoded bodies while retaining PostgreSQL metadata and
search indexes. This is an optimization, not a replacement for the current
canonical article model.

Full-text body search should be evaluated only after normalization and body
retention behaviour are understood. It must remain bounded, optional, and
separate from the required header search path.

## Suggested delivery order

1. Quality flags, normalization versioning, and verification reports.
2. Safe reader improvements and richer MIME/text diagnostics.
3. Standard export, followed by import with provenance.
4. High-confidence thread navigation.
5. Reversible spam/low-value classification.
6. Quote-based relationship inference and optional body full-text search.

## Acceptance themes

- Reprocessing an article is deterministic for a fixed normalizer version.
- A malformed field does not abort an indexing batch.
- Every derived value can be traced to its source article and normalizer.
- Exported content is clearly labelled as stored, decoded, or provider-retrieved.
- Import/export round trips preserve Message-ID, group membership, dates, and
  provenance.
- Cleanup reports are safe to run repeatedly and do not silently delete data.
- Rendering remains safe for hostile or malformed message content.
