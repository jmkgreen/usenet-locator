# Documentation index

This is the mandatory entry point for implementation agents.

## Authority and reading order

1. [`requirements.md`](requirements.md) — authoritative product and non-functional requirements. **MUST/SHALL** requirements take precedence over implementation suggestions elsewhere.
2. [`security-privacy.md`](security-privacy.md) — security, TLS, VPN fail-closed behaviour, privacy, logging, and metrics constraints.
3. [`nntp-indexing.md`](nntp-indexing.md) — NNTP provider model, historical scanning, coverage, checkpoints, and supplementary-provider behaviour.
4. [`data-model.md`](data-model.md) — required information model and persistence semantics.
5. [`architecture.md`](architecture.md) — architectural constraints and decisions left to the implementation agent.
6. [`configuration.md`](configuration.md) — configuration, secrets, PostgreSQL deployment choices, resource controls, logging, and VPN integration.
7. [`testing-acceptance.md`](testing-acceptance.md) — test-quality requirements, coverage gate, and acceptance criteria.
8. [`implementation-plan.md`](implementation-plan.md) — staged delivery order and approval gates.

The repository-level [`../README.md`](../README.md) is orientation only. If it conflicts with a detailed document, the detailed document governs; if two detailed documents conflict, `requirements.md` governs unless it explicitly delegates the decision.

## Rules for an implementation agent

- Begin with **Stage 1 only** and produce a technical design before main implementation.
- Do not silently weaken or replace confirmed requirements.
- Ask for approval when a genuine product decision is unresolved; make ordinary low-level engineering choices independently and document them.
- Human source-code readability is **not a weighted language-selection criterion**. Choose the backend technology for correctness, ecosystem suitability, NNTP capabilities, reliability, resource efficiency, maintainability by automated tooling, and deployability.
- Correctness and reliability take precedence over raw indexing throughput.
- Design for consumer/homelab use. Do not introduce enterprise features such as multi-tenancy, elaborate RBAC, distributed workers, Kubernetes, or audit tracking unless another requirement actually needs them.
- Stage completion requires meaningful tests and the acceptance evidence described in `testing-acceptance.md`.
