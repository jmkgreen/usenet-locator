# User guide

Create an indexing job by choosing one newsgroup, an inclusive start/end date,
and a date margin. It fans out to the enabled provider endpoints. A queued job is claimed by the
bounded dispatcher; a running job can be paused or cancelled. After a process
restart or NNTP failure, a job becomes `interrupted` and must be resumed
explicitly. This is intentional: the application never silently restarts work.
Date-to-article-number discovery is approximate because provider article dates
are not necessarily ordered. Scans include a safety window of at least 100
article numbers (or one batch, whichever is larger) around discovered bounds,
but unusually disordered dates outside that window can still be missed.

Search operates on local headers. Search by subject, author, newsgroup, exact
Message-ID, and/or inclusive UTC start and end dates. A result page continues
the filters submitted with the search even if the form is edited before loading
more; submit again to apply changes. Search filters unwanted articles by default;
select **Include unwanted** to inspect them. Select one or more results to mark
them unwanted or clear that mark. The mark is local, permanent until cleared,
and applies to the same Message-ID wherever it is known.

Article details include available references, byte and line counts, and group
memberships; unknown metadata is labelled rather than guessed.

Opening an article first shows stored headers, group memberships, and cache
state without contacting NNTP. **Retrieve text** is explicit. It reuses cached
text when available; otherwise it requests a bounded body from a known
endpoint-local article location. A marked unwanted article cannot trigger a new
body retrieval until its mark is cleared. Text can be exported as `.txt`.
When fetched, the application also retains the original headers and bounded
body, reports parsing warnings and MIME-part metadata, and offers a sanitized
HTML view where available. Remote resources and active content are removed.
Quotes and signatures can be expanded in the reading view. The evidence panel
shows the original overview, its source, and the normalizer version. Select
**Reprocess stored source** to rebuild derived values locally; older records
whose original overview was not retained cannot be reprocessed this way.

Under **Coverage**, use **Archive verification** to inspect possible issues
such as inconsistent memberships, duplicate cached text, suspicious dates,
and interrupted jobs. The checks are read-only and paginated, and findings can
change between pages while indexing runs. A location without scan coverage can
be an ordinary result of retention discovery; review its context before acting.
Existing unwanted marks can be cleared and do not delete archive data.
The **Invalid Message-IDs** check can open a retained overview that could not
safely become a canonical article. A provider location conflict records when
the same article number was later observed with another Message-ID.

If an account has a configured transfer quota, NNTP traffic is accounted
durably across body downloads and header scans, including discovery probes and
retries. A request or scan that exhausts the allowance stops before doing more
NNTP work; the final request that crosses a limit may already have transferred
its bounded response and is not cached. Actual transferred bytes are recorded,
including failed requests; concurrent operations can exceed the limit together.

The storage/coverage view separates locally stored newsgroups from
endpoint-specific scan evidence. A complete interval on one endpoint is not a
claim of completeness on another provider.

In **Coverage**, enter any newsgroup under **Discover earliest headers** and
submit it to queue a check of each enabled provider. The group need not have
been indexed before. The **Job queue** shows recent discovery and indexing
jobs, their provider states and results, and available controls. It refreshes
every five seconds while Coverage is open. Select **Show group results** to
inspect saved observations and retrieve additional oldest headers.
Queue entries show the job ID, range, scan reason, transfer usage, and provider
status. Pause a running job, resume a paused or interrupted one, or cancel it
where the lifecycle permits. To find a job outside the recent list, enter its ID
under **Find job by ID**. For an indexing job, **Use as source** opens a
supplementary scan with its group, dates, margin, and source ID prefilled;
choose the appropriate reason and, optionally, a transfer budget before queuing.

Each discovery examines at most 1,000 article numbers from the provider's
reported low bound. The result is the earliest usable header observed in that
bounded search, not proof that no older article exists. An empty probe or a
provider failure is shown separately from a successful result. Browsing stored
headers does not contact providers; retrieving additional headers does.

Use **Timeline coverage** to select a group and browse years, then months, then
days. A unit is complete only after every enabled endpoint has successfully
covered it; a gap or pending clue identifies incomplete or active work. Select
**Complete coverage** to queue only outstanding endpoint work. **Go to
articles** opens the locally stored headers from the start of that period in
chronological order and never starts NNTP work. Subsequent pages remain within
the selected inclusive UTC period; **Browse whole group** clears that filter.
Coverage rows show available observed date bounds and reasons as well as the
endpoint-specific article range. The records-per-page selection
is saved in this browser and applies to article lists.

Use the **Retention watchlist** for groups whose retention should be checked
regularly. Choose an interval between one hour and seven days. While the
service is running, due entries receive the same bounded retention probe as
**Find earliest**; the check records the earliest retained header and the
current provider group bounds. Failed checks remain due for a later retry.
Adding, changing, or removing an entry does not contact a provider.

The provider view shows configured endpoints along with active/allowed account
connections, transfer usage, and enabled status. Disabled endpoints keep their
historical evidence but receive no new work. **Show history** selects an
endpoint and displays its qualification history without contacting the provider.
To perform a new compatibility check, optionally supply a Message-ID, group,
and article number, then explicitly select **Run compatibility check**. This
contacts the provider and consumes quota; successful checks refresh history.
The provider view never exposes credentials or raw provider responses.
