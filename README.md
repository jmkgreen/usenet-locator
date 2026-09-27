# Historical Usenet Indexer

Requirements and implementation guidance for a self-hosted, consumer/homelab historical Usenet header indexer.

## Start here

Implementation agents **must read [`docs/index.md`](docs/index.md) first**. It defines the document set, authority, implementation order, and links to the detailed requirements.

The first usable release focuses on:

- historical NNTP header indexing for selected newsgroups and date ranges;
- PostgreSQL persistence;
- multiple configurable NNTP providers/endpoints;
- a REST/JSON API with OpenAPI documentation;
- a lightweight SPA for search, text reading, and basic job controls;
- on-demand text article retrieval and decoded-text caching;
- recoverable, manually resumable indexing jobs;
- conservative resource use suitable for a shared homelab server.

Multipart binary grouping, NZB generation/SABnzbd submission, advanced supplementary-provider scanning, and MCP integration are later stages as described in the documentation.

## Documentation

See [`docs/index.md`](docs/index.md).
