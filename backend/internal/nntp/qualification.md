# NNTP qualification matrix

An implementation must pass scripted tests for all of the following before it
is used by indexing or retrieval:

- implicit TLS with hostname/certificate verification and no plaintext fallback;
- greeting and authentication failures;
- `CAPABILITIES` variance and optional `MODE READER` behaviour;
- `LIST OVERVIEW.FMT` support and the recognised standard overview fields;
- `STAT <message-id>` against a known article, before attempting an overview
  command for that article number in the selected group;
- `GROUP`, `OVER`, and `XOVER` fallback;
- sparse article ranges, malformed overview fields, and truncated dot blocks;
- RFC 5322 overview-date variants, including valid dates without a weekday;
- read/connect timeout and context cancellation;
- dot-stuffed multiline responses and bounded streaming article-body reads.

The current package supplies the implementation-independent model and parser
tests. Candidate library adapters must add the protocol-fixture tests above;
no unqualified library may be used by a job worker.

## Candidate results

`github.com/go-newsgroups/nntp v0.1.0` is **rejected for Stage 1**. It provides
implicit TLS, authentication, group selection, and `OVER`/`XOVER`, but its
public API lacks `CAPABILITIES`, `MODE READER`, and streaming body/header
operations. It also materialises full articles. The qualification adapter
returns an explicit unsupported error for the missing operations so it cannot
be promoted accidentally.

`github.com/dustin/go-nntp` at `f00d51cf8cc1` is also **rejected for Stage
1**. It has a stronger command surface (`CAPABILITIES`, `STARTTLS`, `HEAD`,
`BODY`, `ARTICLE`, and dot-stream readers), but its connection methods do not
accept contexts or expose deadline configuration. The application therefore
cannot reliably cancel a blocked read for pause, shutdown, or resource-pressure
handling. Its upstream client package also has no tests. Command availability
does not compensate for the lack of bounded-operation control.

The next qualification step is a small internal reader client over `net.Conn`,
`crypto/tls`, and `net/textproto`, with explicit deadlines and transcript tests.
It implements only the Stage 1 reader commands; it is not an NNTP server or
posting client.
