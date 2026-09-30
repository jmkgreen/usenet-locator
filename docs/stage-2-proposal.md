# Stage 2 proposal: provider expansion and MCP

Stage 1 established a working local indexer, including a successful historical
scan on a real provider. Stage 2 improves interoperability and makes the local
index useful to MCP clients without widening the trust boundary by default.

## Objectives

1. Make NNTP provider compatibility explicit, repeatable, and endpoint-local.
2. Add supplementary-provider scans only when they answer a defined coverage
   question; never treat one endpoint's article numbers as another's.
3. Expose a small MCP surface over existing services, with read and write tools
   clearly separated and protected.

## Provider compatibility profiles

Keep a versioned, non-secret profile per endpoint. It records only observed
protocol facts and selected workarounds: overview preference (`auto`, `over`,
or `xover`), reader-mode behaviour, recognised overview fields, redacted date
parsing fixtures, and safe qualification command/status outcomes.

Profiles are evidence, not provider-name rules. The Stage 1 finding becomes the
first fixture: a valid RFC 5322 overview date without a weekday. The generic
parser accepts this through `mail.ParseDate`; the profile preserves why the
test exists. A failed `STAT` is reported independently from `GROUP`/`OVER`, as
some providers permit overview indexing while restricting Message-ID lookup.

## Supplementary coverage strategy

Do not mirror every primary scan. Instead, introduce an explicit supplementary
scan request with a target endpoint/newsgroup, a date interval or primary
endpoint-local gap, a reason (`missing_range`, `failed_batch`, or
`operator_requested`), and a conservative budget separate from account quota.

Workers resolve date intervals independently per endpoint. Article-number gaps
remain source-local and must never be sent unchanged to another provider.
Canonical deduplication stays on Message-ID; every recovered article retains a
distinct endpoint/newsgroup/location record and coverage interval.

Initial completion criteria: no automatic scans, endpoint-labelled coverage,
separate recovered/unavailable results, and Stage 1-equivalent quota,
interruption, and checkpoint behaviour.

## MCP boundary

Implement MCP as a separate opt-in listener or authenticated reverse-proxy
route. Use stateless Streamable HTTP and a pinned, reviewed Go MCP SDK.

Read-only tools are `search_headers`, `get_article_headers`,
`list_newsgroups`, `list_coverage`, `list_provider_status`, and `get_index_job`.
They reuse application services and never fetch bodies or send NNTP commands.

State-changing tools are `create_index_job`, job controls,
`set_articles_unwanted`, `retrieve_article_text`, `run_provider_preflight`, and
future supplementary-scan creation. They require a separate
`usenet-locator.write` scope. Tool annotations may describe risk, but server
authorization and validation are authoritative. NNTP-affecting tools retain
quota/connection controls and never accept raw commands, credentials, or hosts.

For the initial homelab deployment, a reverse proxy authenticates the MCP route
and injects a validated identity/scope header from a configured trusted proxy
network. Direct spoofed headers are rejected. Internet-facing deployments use
OAuth 2.1/OIDC with issuer/audience validation and explicit scopes.

The current MCP specification supports a stateless core and strengthened
authorization; its tool annotations are advisory. References: [MCP 2026-07-28
release](https://blog.modelcontextprotocol.io/posts/2026-07-28/), [tool
annotation guidance](https://blog.modelcontextprotocol.io/posts/2026-03-16-tool-annotations/),
and the [Go SDK security guide](https://go.sdk.modelcontextprotocol.io/protocol/).

## Delivery order

1. Profile persistence/configuration, redacted transcript fixtures, and
   qualification-history API/UI.
2. Targeted supplementary-scan jobs, tested with a second endpoint.
3. Read-only MCP behind a feature flag.
4. Scoped write tools with cost controls and integration tests.

Each step has its own review. No external listener, provider account, or
supplementary scan is enabled by default.
