# Security, privacy, logging, VPN and metrics

## 1. Security model

The intended deployment is a trusted LAN/homelab. End-user application security can therefore remain minimal initially.

The application is expected to sit behind an HTTPS reverse proxy with authentication. No application-level user account/RBAC system is required for Stage 1. The deployment SHALL avoid unintentionally exposing an unauthenticated application port outside the intended trusted network/container topology.

This LAN-oriented security posture does **not** weaken outbound NNTP transport requirements.

## 2. NNTP TLS

- TLS SHALL be required by default for NNTP endpoints, using normal certificate/hostname validation.
- There SHALL be no automatic downgrade from TLS to plaintext if TLS fails.
- Plaintext NNTP SHALL require an explicit per-endpoint user override/acknowledgement.
- A TLS failure should cause the endpoint to fail visibly so the user checks host, port, TLS mode, credentials, certificates, or provider settings rather than the application silently weakening transport security.
- Credentials and secrets SHALL never be written to normal logs, metrics, API responses, or frontend state.

## 3. External VPN routing

The application SHALL support deployment where all application traffic destined for the outside world is routed through a single external VPN container supplied/managed by the user (for example, a container already present in the user's Docker stack).

This is whole-application external routing, not per-provider routing.

### Fail-closed requirement

If VPN routing is configured:

- loss/unavailability of the VPN SHALL NOT cause NNTP or other external application traffic to fall back to the host's normal WAN path;
- affected external operations SHALL fail/pause;
- VPN-unavailable state SHALL be clearly visible in logs and on-screen status;
- normal direct routing may resume only after the user disables the configured VPN mode or the configured VPN path becomes healthy again.

The implementation design SHALL explain how the Docker/network topology enforces this property rather than relying solely on application intent.

## 4. Privacy and activity data

The system SHALL minimise records of end-user activity.

Do not persist:

- search history or search terms for behavioural/audit purposes;
- a history of articles viewed by a user;
- user navigation/activity trails;
- analytics/telemetry about end-user behaviour.

Operational state needed to perform the requested work is allowed. Examples include a Message-ID currently being fetched, job checkpoint information, provider errors, or identifiers needed to retry a failed operation.

Operational data SHALL not be repurposed into an end-user activity history.

No external analytics or telemetry service is required or expected.

## 5. Logging

### Default

- Normal container logging to stdout/stderr is sufficient by default.
- Support an optional configured file/directory log destination suitable for a Docker volume mount so logs can be retained for retrospective inspection.
- File logging SHALL include sensible rotation/retention controls to avoid unbounded disk growth.
- A future API/UI for searching logs is explicitly out of current scope.

### Levels

At minimum support a normal operational level and a more detailed debug level. The exact logging framework/level names are implementation choices.

Normal logs should focus on:

- startup/configuration outcome without secrets;
- job lifecycle and progress-relevant events;
- provider/VPN/TLS/connectivity failures;
- resource-pressure warnings/throttling/failure;
- database/queue failures necessary for diagnosis.

Debug mode may include deeper NNTP protocol/operation context where useful, but SHALL still avoid credentials and SHALL minimise article/user content. Debug logging is not permission to record search terms or create browsing histories.

## 6. Metrics

Expose lightweight operational metrics. Implementation may use a conventional `/metrics` endpoint or an equivalent low-overhead mechanism.

Useful metrics include:

- application process memory and CPU;
- resource-pressure/throttling state;
- active/queued indexing jobs;
- indexing throughput and error counts;
- active NNTP connections by endpoint/provider;
- provider health/status and failure counts;
- transferred bytes/usage against configured provider/account quotas where known;
- database connection-pool utilisation;
- relevant queue/batch depths.

### Metrics privacy rule

Metrics SHALL contain operational aggregates only. Do not place subjects, authors, Message-IDs, search terms, article content, or other user/article content into metric names or labels.

The web UI SHALL provide a lightweight provider-status page/list based on appropriate operational state, without requiring a heavy monitoring stack.

## 7. Secrets

Application settings belong in configuration files where practical. Credentials, API keys, and similar secrets SHALL be supplied separately through environment variables or Docker secrets (or a comparably appropriate mechanism selected by the implementation agent).

The documentation SHALL explain secret injection for both the reference Docker deployment and a typical TrueNAS/homelab deployment.
