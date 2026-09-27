# Architecture guidance

## 1. Decisions deliberately left open

The implementation agent selects the backend language/framework, NNTP library/implementation, frontend framework within the SPA requirement, database access layer, migration system, and internal job architecture.

The design proposal must explain why the chosen stack is suitable for:

- robust NNTP/TLS support;
- historical batched retrieval;
- controlled concurrency and account connection limits;
- durable job/checkpoint handling;
- PostgreSQL;
- low/controllable memory use;
- REST/OpenAPI;
- Docker/TrueNAS deployment;
- automated testing and future MCP integration.

Human source-code readability is not a selection criterion. Do not choose a less suitable stack merely because a human developer might find it more familiar/readable.

## 2. Resource-conscious architecture

Prefer bounded designs: bounded queues, bounded connection pools, streaming/iterative processing where appropriate, and explicit concurrency limits. Avoid loading entire historical ranges or large result sets into memory.

Expose resource utilisation and throttling state to the UI/API. Resource pressure should warn, then throttle where possible, then fail safely if necessary rather than risking corrupted state.

## 3. API and frontend

REST/JSON API is the primary application interface and SHALL have OpenAPI documentation. Business/indexing logic should not be embedded exclusively in the frontend so later CLI and MCP interfaces can reuse it.

The SPA initially focuses on:

- job creation and basic controls;
- job progress;
- header search/results;
- article detail/text reader;
- provider/resource status.

Use polling for progress updates in Stage 1.

## 4. Deployment topology

Reference deployment:

- application container;
- PostgreSQL container;
- persistent volumes as appropriate.

Supported alternative:

- application container connects to an externally managed/shared PostgreSQL instance.

The application may be placed behind an existing reverse proxy. If external VPN routing is configured, Docker/network topology must enforce fail-closed routing as described in `security-privacy.md`.

## 5. MCP

MCP is a post-REST/web feature. It should expose selected existing application capabilities rather than create another indexing implementation.

Candidate read operations include header search, article retrieval, references, job status, and coverage. State-changing MCP operations (for example creating/pausing jobs) should be explicitly designed before exposure.
