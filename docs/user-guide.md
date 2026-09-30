# User guide

Create an indexing job by choosing a configured endpoint, one newsgroup, an
inclusive start/end date, and a date margin. A queued job is claimed by the
bounded dispatcher; a running job can be paused or cancelled. After a process
restart or NNTP failure, a job becomes `interrupted` and must be resumed
explicitly. This is intentional: the application never silently restarts work.

Search operates on local headers. It filters unwanted articles by default;
select **Include unwanted** to inspect them. Select one or more results to mark
them unwanted or clear that mark. The mark is local, permanent until cleared,
and applies to the same Message-ID wherever it is known.

Opening an article first shows stored headers, group memberships, and cache
state without contacting NNTP. **Retrieve text** is explicit. It reuses cached
text when available; otherwise it requests a bounded body from a known
endpoint-local article location. A marked unwanted article cannot trigger a new
body retrieval until its mark is cleared. Text can be exported as `.txt`.

If an account has a configured transfer quota, NNTP traffic is accounted
durably across body downloads and header scans, including discovery probes and
retries. A request or scan that exhausts the allowance stops before doing more
NNTP work; the final request that crosses a limit may already have transferred
its bounded response and is not cached.

The storage/coverage view separates locally stored newsgroups from
endpoint-specific scan evidence. A complete interval on one endpoint is not a
claim of completeness on another provider.

The provider view shows configured endpoints along with active/allowed account
connections and transfer usage. It never exposes provider credentials.
