# Deployment

## Reference Compose deployment

Copy `deploy/config.example.json` to a private configuration file and replace
the placeholder NNTP host and endpoint ID. Copy `deploy/compose.yaml` to
`deploy/compose.local.yaml` and replace its generic provider secret names with
the names used by the private configuration. Create the private
`deploy/secrets/` directory containing one newline-terminated file per secret.
The stack needs `postgres-password`, `database-url`, plus every NNTP username
and password file named by your configuration. `database-url` is the app's
PostgreSQL connection string and must use the same password as
`postgres-password` when connecting to the bundled database. Do not commit the
local Compose file, configuration, or any secret files. Start the local stack
with:

```text
docker compose -f deploy/compose.local.yaml up --build
```

The reference ports bind to loopback only: the application is on `8080` and
PostgreSQL is on `5432`. Put the application behind an authenticated HTTPS
reverse proxy before making it available on a LAN. The database is intentionally
not exposed on a public interface.

The application applies its embedded migrations at startup. Back up the
PostgreSQL volume before upgrades and verify `/readyz` after deployment.

## Shared PostgreSQL

For an external PostgreSQL instance, omit the Compose `db` service and mount a
file containing an application-scoped connection string. Point `database.url_file`
at that mounted path. The application only creates its own tables and migration
ledger; it does not alter server-wide PostgreSQL settings. Keep the pool small
(`max_conns: 4` is the reference value) when the database is shared.

## Secrets and TLS

Configuration names secret-file paths but never contains their values. Supply
the database URL and each NNTP account's username/password as separate mounted
secret files; Docker Compose, TrueNAS, and Kubernetes all support this model.
Provider account IDs and endpoint details remain ordinary private configuration,
which allows one account to own multiple endpoints without multiplying secret
variables. The application requires TLS unless the specific endpoint has an
explicit plaintext acknowledgement. It never retries a failed TLS connection as
plaintext.

Most endpoints use the default overview behaviour (`"overview_command":
"auto"` or omitted), which sends `OVER` and has a bounded legacy fallback.
If a provider is known to accept only legacy `XOVER`, set
`"overview_command": "xover"` on that private endpoint configuration; this
avoids sending `OVER` first.

## Provider qualification

Before beginning a historical scan on a newly configured endpoint, issue the
bounded local preflight request:

```text
POST /api/v1/providers/{endpoint-id}/preflight
{"newsgroup":"alt.test"}
```

It opens one authenticated, quota-accounted connection and checks
`CAPABILITIES`, `MODE READER`, `LIST OVERVIEW.FMT`, `GROUP`, and one `OVER`
record. An optional `message_id` performs a separate `STAT` check; do not
interpret an unavailable `STAT` result as evidence that overview indexing is
unavailable. An optional `article_number` lets an operator probe one specific
number inside the selected group. The response intentionally contains only
fixed capability names, numeric NNTP outcomes, group bounds, and counts of
valid/dated overview records—never credentials, message IDs, or provider
response text.

The preflight is a diagnostic action, not a scan: it does not persist articles
or coverage. It consumes the configured account transfer quota and connection
permit, so run it sparingly and keep the service bound to loopback or behind an
authenticated reverse proxy.

Successful preflights are retained as safe endpoint qualification history. Read
the latest records with `GET /api/v1/providers/{endpoint-id}/qualifications`.
They exclude group names, Message-IDs, credentials, and raw provider response
text.

### Date compatibility

NNTP overview dates are mail dates, not necessarily strict RFC 1123 dates.
The client uses RFC 5322-compatible parsing, including valid provider records
that omit a weekday. If a qualification response reports overview rows but no
parseable dates, stop before launching a broad scan and add a transcript test
for the observed date form.

## Provider fan-out and supplementary scans

Every normal header-range request creates one non-dispatchable parent record
and one endpoint-local child job for each enabled configured provider account.
The parent status aggregates the child counts and states; child failures do not
discard headers already retained from another provider. The dispatcher applies
the existing per-account connection permits and transfer quotas independently
to each child. Removed endpoints are disabled during configuration sync and
receive no new fan-out work, while their historical coverage remains visible.

An optional request-level transfer budget is divided evenly among the child
jobs (and must be at least one byte per enabled endpoint), so the combined
child allowance never exceeds the requested budget.

Create a supplementary scan only when an operator identifies a specific
endpoint-local missing range or failed batch. Provide the original job ID as the
source job and set a conservative optional budget. The budget is additional to
the provider account quota: each child stops with `job transfer budget reached`
when its allotted measured NNTP traffic crosses the allowance. Article numbers
are never reused across providers; each endpoint independently resolves the
requested date range and records separate coverage and locations.

## VPN mode

When a user-managed VPN gateway is required, attach the application to that
container's network namespace (for example, Docker `network_mode:
service:vpn`) and ensure the gateway provides a tested kill switch. Stop the
gateway and confirm an NNTP connection cannot leave the normal host route
before relying on the deployment. The application should then show failed or
interrupted external work rather than silently using WAN fallback.
