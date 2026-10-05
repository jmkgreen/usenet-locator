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

Use **Timeline coverage** to select a group and browse years, then months, then
days. A unit is complete only after every enabled endpoint has successfully
covered it; a gap or pending clue identifies incomplete or active work. Select
**Complete coverage** to queue only outstanding endpoint work. **Go to
articles** opens the locally stored headers from the start of that period in
chronological order and never starts NNTP work. The records-per-page selection
is saved in this browser and applies to article lists.

Use the **Retention watchlist** for groups whose retention should be checked
regularly. Choose an interval between one hour and seven days. While the
service is running, due entries receive the same bounded retention probe as
**Find earliest**; the check records the earliest retained header and the
current provider group bounds. Failed checks remain due for a later retry.
Adding, changing, or removing an entry does not contact a provider.

The provider view shows configured endpoints along with active/allowed account
connections and transfer usage. It never exposes provider credentials.
