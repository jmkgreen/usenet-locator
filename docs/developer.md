# Developer guide

Use Go 1.27 and Node 24 with the pinned pnpm version. Do not add frontend
packages without an exact version, lockfile update, and audit. The workspace
requires a minimum package release age and permits no lifecycle build scripts.

Run the normal verification commands from the repository root:

```text
go test ./backend/...
pnpm --dir frontend test -- --run
pnpm --dir frontend build
pnpm --dir frontend audit --audit-level=high
```

The frontend coverage gate is 80% lines/statements and includes the application
component; the executable React entrypoint is excluded because it contains no
application behavior. CI additionally runs Go race/coverage checks on Linux.
It also builds the reference Compose stack with disposable credentials and
checks both `/readyz` and the served SPA; this smoke test never queues NNTP
work.

NNTP protocol changes require scripted transcript tests. Do not log
credentials, article content, Message-IDs, authors, subjects, or search terms.
Use the narrow client interface and preserve bounded streaming behavior.

NNTP usage is charged through the account quota store using an atomic
PostgreSQL conditional update. The wire client measures socket reads and writes
(including TLS records); scanner probes and retries are charged from that
meter, never approximated from article header metadata. Keep the
`ErrQuotaExceeded` mapping distinct from NNTP transport failures.
