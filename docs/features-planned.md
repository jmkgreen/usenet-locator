# Features planned

This is a status-oriented guide to future capabilities, not a release schedule.
See the [product overview](../README.md) for the intended audience and
[features implemented](features-implemented.md) for what the current source
tree actually provides. The detailed [implementation plan](implementation-plan.md)
contains delivery stages and historical design work; entries there are not, by
themselves, evidence that a feature is unimplemented or a stage is accepted.

## Confirmed later requirements

These capabilities are identified as later requirements in the
[product requirements](requirements.md); their design, delivery order and
acceptance remain subject to the project's stage process.

### Binary and NZB workflows

- Optional multipart binary grouping, identifying missing segments, NZB
  generation, and submission to SABnzbd. Individual article results remain
  available. See [requirements](requirements.md#9-binary-grouping-and-nzb--later-enhancement)
  and [Stage 3](implementation-plan.md#stage-3--binarynzb-functionality).

### Provider research and automation

- More advanced supplementary-provider scanning, including identifying and
  checking holes or failed batches across providers and optional cross-provider
  article availability checks. Manual source-linked scans already exist; this
  does not imply that automatic hole discovery exists today. See
  [supplementary requirements](requirements.md#53-supplementary-providers--later-capability)
  and the [provider expansion proposal](stage-2-proposal.md).
- MCP access to existing application services, after its operation boundaries
  and access controls are defined. See [Stage 2](implementation-plan.md#stage-2--researchprovider-expansion-and-mcp).

### Deletion

- Deletion scopes for an individual canonical article, a date range in a
  group, and a whole group, with crossposts, cached bodies and coverage handled
  deliberately. The workflow and retention consequences require review; an
  unwanted mark is not deletion. See [deferred deletion policy](requirements.md#14-deferred-deletion-policy).

## Proposals awaiting approval

### Archive quality, reading, and interoperability

The [archive quality proposal](archive-quality-proposal.md) covers reversible
normalization and quality flags, verification, safer article rendering,
thread navigation, and standard import/export. These are proposals, not claims
of current availability. Access to individual attachments in MIME/multipart
articles is a [product requirement](requirements.md#7-text-article-retrieval)
but is not listed as an implemented UI workflow.

### Provider deletion observability

The [provider deletion observability proposal](provider-deletion-observability-proposal.md)
would record provider availability history and support research into missing or
deleted content without deleting the local archive.

## Optional ideas and remaining requirements

- A dedicated CLI, quote-based thread inference, log-search UI/API, and optional
  full-text body search are [optional future ideas](implementation-plan.md#stage-7--optional-enhancements).
- The [requirements](requirements.md) also describe behaviours beyond this
  feature catalogue, such as richer progress/resource reporting and attachment
  access. A requirement or proposal is not proof of implementation; consult
  the [implemented catalogue](features-implemented.md) and current source for
  the present scope.
