# Historical Usenet Indexer

A self-hosted research tool for building a local index of Usenet headers and
reading historical text discussions. It is intended for personal or homelab
use with your own NNTP provider accounts and PostgreSQL database.

## Is this for you?

- **Research selected groups:** index headers for a chosen newsgroup and date
  range, then search your local collection by subject, author, Message-ID,
  group, or date. [What works today](docs/features-implemented.md#search-and-read)
- **Work across providers:** use multiple configured endpoints, keep their
  coverage evidence separate, and merge known articles by Message-ID.
  [What works today](docs/features-implemented.md#providers-and-coverage)
- **Explore retained history:** check a group's earliest observed headers at
  each enabled provider and browse locally stored headers by date. The check
  is bounded; it does not prove the beginning of Usenet history.
  [What works today](docs/features-implemented.md#retained-history-and-watchlist)
- **Read on demand:** inspect headers first, explicitly retrieve text when
  needed, cache it locally, and export it as plain text.
  [What works today](docs/features-implemented.md#search-and-read)
- **Control background work:** view provider progress and manage durable
  indexing and discovery jobs from the web console.
  [What works today](docs/features-implemented.md#indexing-and-job-control)

This is **not currently a binary download manager**: multipart grouping, NZB
generation, and SABnzbd submission are [planned](docs/features-planned.md#binary-and-nzb-workflows).
It is also not a hosted Usenet service; you supply the server, database, and
provider accounts. Availability depends on what your providers retain.

## Feature status and documentation

- [Features implemented](docs/features-implemented.md) describes capabilities
  present in this source tree, including important limitations. A deployed
  image may differ depending on its version.
- [Features planned](docs/features-planned.md) separates confirmed later
  requirements from proposals and optional ideas. Plans are not promises of a
  release date.
- [User guide](docs/user-guide.md) explains how to use the current workflows;
  [deployment](docs/deployment.md) covers installation and security.
- [Documentation index](docs/index.md) links the complete requirements, design,
  implementation plan, and contributor guidance. Implementation agents must
  read it first.
