# Deployment

## Reference Compose deployment

Copy `deploy/config.example.json` to a private configuration file and replace
the placeholder NNTP host and endpoint ID. Create a private `.env` beside the
Compose file with `POSTGRES_PASSWORD`, `NNTP_USERNAME`, and `NNTP_PASSWORD`.
Do not commit either file. Start the reference stack with:

```text
docker compose -f deploy/compose.yaml up --build
```

The reference ports bind to loopback only: the application is on `8080` and
PostgreSQL is on `5432`. Put the application behind an authenticated HTTPS
reverse proxy before making it available on a LAN. The database is intentionally
not exposed on a public interface.

The application applies its embedded migrations at startup. Back up the
PostgreSQL volume before upgrades and verify `/readyz` after deployment.

## Shared PostgreSQL

For an external PostgreSQL instance, omit the Compose `db` service and set
`USENET_LOCATOR_DATABASE_URL` to an application-scoped connection string. The
application only creates its own tables and migration ledger; it does not alter
server-wide PostgreSQL settings. Keep the pool small (`max_conns: 4` is the
reference value) when the database is shared.

## Secrets and TLS

Configuration names environment variables but never contains their values.
Supply the database URL and NNTP username/password with Docker/TrueNAS secrets
or private environment variables. The application requires TLS unless the
specific endpoint has an explicit plaintext acknowledgement. It never retries a
failed TLS connection as plaintext.

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

## VPN mode

When a user-managed VPN gateway is required, attach the application to that
container's network namespace (for example, Docker `network_mode:
service:vpn`) and ensure the gateway provides a tested kill switch. Stop the
gateway and confirm an NNTP connection cannot leave the normal host route
before relying on the deployment. The application should then show failed or
interrupted external work rather than silently using WAN fallback.
