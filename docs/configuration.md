# Configuration and deployment requirements

## 1. Configuration model

Use configuration files for ordinary application settings. Supply secrets separately through environment variables, Docker secrets, or another documented secret mechanism appropriate to the selected stack.

Configuration should cover at least:

- accounts and credentials references;
- NNTP endpoints, ports, TLS/plaintext acknowledgement, and primary/supplementary role;
- account connection limits;
- metered account limits;
- indexing batch override and concurrency/resource limits;
- date-boundary margin;
- PostgreSQL connection settings;
- SABnzbd API settings when that feature is implemented;
- VPN-enabled deployment mode/integration;
- logging level and optional persistent log path/rotation;
- lightweight metrics enablement/exposure.

## 2. PostgreSQL options

Documentation SHALL provide two supported patterns.

### Bundled/reference PostgreSQL

Provide an example/reference Docker deployment containing PostgreSQL with persistent storage. Include a conservative/tight example suitable for a constrained homelab and a looser example for a host with more available resources. Exact values should be justified by the selected PostgreSQL/application stack rather than hard-coded here prematurely.

### Existing/shared PostgreSQL

Document how to:

- create/provide a database and application role;
- provide the connection URI/settings and secret safely;
- run schema migrations;
- configure a bounded application connection pool;
- back up/restore application data at a practical level;
- upgrade without assuming the application controls the PostgreSQL server.

The application SHALL not modify global PostgreSQL server configuration automatically.

## 3. Resource controls

Document both:

- application-level worker/concurrency/connection controls;
- Docker/TrueNAS CPU/memory limits as hard outer limits.

The UI should show enough memory/resource status to help diagnose a configuration that is too tight.

## 4. VPN deployment

Document a deployment mode where the application's outside-world traffic uses a user-provided VPN container/network path. The exact example may depend on the selected Docker approach, but it must demonstrate fail-closed behaviour and how the UI/application determines that the VPN path is unavailable.

No per-provider VPN routing is required.

## 5. Logging

Default to stdout/stderr. Document optional persistent file logging using a volume-mounted path, including rotation/retention configuration.

## 6. Reverse proxy

Document the expected reverse-proxy arrangement and trusted-LAN assumption without requiring a specific proxy product. Application-level authentication is not required in Stage 1.
