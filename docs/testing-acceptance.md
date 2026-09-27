# Testing and acceptance

## 1. Testing philosophy

Tests must be meaningful. Code coverage is a quality gate, not the purpose of the test suite.

The build/CI SHALL fail when measured code coverage is below **80%** for the agreed coverage metric(s). The implementation agent SHALL document exactly which metric/tool is enforced (for example line/instruction coverage) and any justified exclusions such as generated code.

The 80% threshold SHALL NOT be satisfied through low-value tests that merely execute lines without asserting behaviour. Tests should emphasise externally meaningful behaviour, invariants, failure handling, boundary conditions, idempotency, and recovery.

Where practical, prefer tests that would fail for plausible regressions rather than tests coupled only to internal implementation details.

## 2. Required automated test areas

At minimum cover:

- date-range and boundary/margin handling;
- NNTP article-number discovery behaviour with sparse/missing ranges;
- Message-ID deduplication;
- crosspost membership;
- endpoint-specific article locations;
- account connection/quota enforcement;
- TLS configuration/default rejection of silent plaintext fallback;
- checkpoint durability and idempotent batch retry;
- pause/resume;
- interrupted/partial batch handling;
- search filters and pagination;
- on-demand body caching behaviour;
- individual and batch unwanted marking, filtering, persistence, and retrieval
  suppression;
- privacy-sensitive logging/metrics constraints where reasonably testable;
- resource-pressure state transitions/throttling logic where reasonably testable;
- API validation/error behaviour.

Use mocks/fakes/test NNTP fixtures for deterministic protocol/error scenarios; live-provider tests complement rather than replace automated tests.

## 3. Stage 1 real-world acceptance

Initial live test: a small, recent date range in a known newsgroup.

Historical correctness test: once a suitable historical group/range and known Message-IDs are supplied, verify those known Message-IDs appear after indexing that range.

Performance: no fixed throughput target. Correctness/reliability matter more. Report observed throughput and resource usage so practicality can be assessed on the target hardware.

## 4. Stage 1 acceptance checklist

- [ ] Application starts on the target/reference Docker deployment with persistent data.
- [ ] Application can use an externally managed PostgreSQL instance using documented configuration.
- [ ] A job can be created for one group and an inclusive date range.
- [ ] Appropriate endpoint-local historical article ranges are discovered without assuming cross-provider number equivalence.
- [ ] A small recent live scan produces searchable stored headers.
- [ ] Known historical Message-IDs are found during the later historical validation test.
- [ ] Re-scanning successful coverage avoids unnecessary work and does not duplicate canonical articles.
- [ ] Pause/resume preserves committed progress.
- [ ] Interrupted jobs remain manually resumable after restart rather than silently restarting.
- [ ] Search by subject, author, Message-ID, newsgroup, and date range works with pagination/sorting/filtering.
- [ ] Opening an uncached text article fetches it on demand and subsequent access can use cached decoded text.
- [ ] A user can mark one or a selected batch of articles unwanted; normal
  results exclude them, the explicit filter includes them, and a marked
  uncached article is not retrieved until it is unmarked.
- [ ] Plain-text reader preserves formatting and `.txt` export works.
- [ ] Provider-status UI exposes useful lightweight operational state.
- [ ] Resource utilisation is visible enough to diagnose an overly constrained deployment.
- [ ] REST API has usable OpenAPI documentation.
- [ ] Secrets are not exposed in API/UI/logs/metrics.
- [ ] Metrics contain operational aggregates and no subjects, Message-IDs, authors, search terms, or article content as labels/data intended for monitoring.
- [ ] TLS is required by default; plaintext requires explicit configuration and there is no automatic downgrade.
- [ ] When VPN mode is configured, VPN loss is visible and external traffic does not fall back to the normal WAN route.
- [ ] CI/build enforces >=80% meaningful code coverage.

## 5. Stage review

At the end of each implementation stage provide:

- automated test results and coverage report;
- live/manual acceptance evidence relevant to that stage;
- measured resource usage/throughput where useful;
- known limitations and unresolved issues;
- documentation changes.

Do not begin the next stage until the user approves the current one.
