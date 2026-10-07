import { FormEvent, useEffect, useRef, useState } from "react";
import "./styles.css";
import { applicationName } from "./app-info";

type JobState = "queued" | "running" | "paused" | "interrupted" | "completed" | "failed" | "cancelled";
type ProviderJob = { endpoint: string; state: JobState; headers_retrieved: number; articles_stored: number; last_error?: string | null };
type Job = { id: string; newsgroup: string; endpoint: string; state: JobState; headers_retrieved: number; articles_stored: number; scan_reason?: string; source_job_id?: string | null; transfer_limit_bytes?: number | null; transfer_used_bytes?: number; last_error?: string | null; provider_jobs?: ProviderJob[] };
type Article = { id: number; message_id: string; subject: string; author: string; date?: string | null; unwanted: boolean };
type SearchPage = { articles: Article[]; next_cursor: string; total_records: number; page_size: number };
type ArticleDetail = Article & { references: string; bytes?: number | null; lines?: number | null; newsgroups: string[]; cached_body: boolean };
type Endpoint = { id: string; account_id: string; host: string; port: number; tls: boolean; primary: boolean; priority: number; connection_in_use: number; connection_limit: number; transfer_used_bytes: number; transfer_limit_bytes: number | null };
type GroupCount = { name: string; articles: number };
type RetentionObservation = { endpoint: string; newsgroup: string; group_low: number; group_high: number; article_number?: number; article_id?: number; observed_date?: string; outcome: string; observed_at: string };
type Coverage = { endpoint: string; newsgroup: string; state: string; article_number_start: number; article_number_end: number };
type Qualification = { endpoint: string; result: { capabilities: string[]; overview_format_code: number; overview_fields: string[]; overview_code: number; overview_rows: number; overview_dates: number }; created_at: string };
type WatchlistItem = { newsgroup: string; interval_hours: number; last_checked_at?: string | null; next_check_at: string };
type TimelinePeriod = { level: "year" | "month" | "day"; start_date: string; end_date: string; article_count: number; state: "complete" | "pending" | "gaps"; endpoints: { endpoint: string; state: string }[] };
type View = "index" | "search" | "coverage" | "watchlist" | "settings";
type Theme = "system" | "light" | "dark";
const api = "/api/v1";

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`;
}

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const response = await fetch(`${api}${path}`, { headers: { "Content-Type": "application/json" }, ...options });
  if (!response.ok) {
    const body = await response.json().catch(() => ({ error: "Request failed" })) as { error?: string };
    throw new Error(body.error ?? `Request failed (${response.status})`);
  }
  if (response.status === 204) return undefined as T;
  return response.json() as Promise<T>;
}

export function App() {
  const [activeView, setActiveView] = useState<View>("index");
  const [theme, setTheme] = useState<Theme>(() => (localStorage.getItem("usenet-locator.theme") as Theme | null) ?? "system");
  const detailReturnFocus = useRef<HTMLElement | null>(null);
  const [newsgroup, setNewsgroup] = useState("");
  const [startDate, setStartDate] = useState("");
  const [endDate, setEndDate] = useState("");
  const [marginDays, setMarginDays] = useState(2);
  const [scanReason, setScanReason] = useState("operator_requested");
  const [sourceJobID, setSourceJobID] = useState("");
  const [transferLimit, setTransferLimit] = useState("");
  const [job, setJob] = useState<Job | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [searchSubject, setSearchSubject] = useState("");
  const [searchAuthor, setSearchAuthor] = useState("");
  const [searchGroup, setSearchGroup] = useState("");
  const [includeUnwanted, setIncludeUnwanted] = useState(false);
  const [articles, setArticles] = useState<Article[]>([]);
  const [nextCursor, setNextCursor] = useState("");
  const [searchTotal, setSearchTotal] = useState(0);
  const [pageSize, setPageSize] = useState(() => Number(localStorage.getItem("usenet-locator.page-size")) || 50);
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const [detail, setDetail] = useState<ArticleDetail | null>(null);
  const [bodyText, setBodyText] = useState("");
  const [endpoints, setEndpoints] = useState<Endpoint[]>([]);
  const [groups, setGroups] = useState<GroupCount[]>([]);
  const [coverage, setCoverage] = useState<Coverage[]>([]);
  const [qualifications, setQualifications] = useState<Qualification[]>([]);
	const [retentionGroup, setRetentionGroup] = useState("");
	const [retention, setRetention] = useState<RetentionObservation[]>([]);
	const [oldestHeaderCount, setOldestHeaderCount] = useState(10);
	const [chronological, setChronological] = useState<SearchPage>({ articles: [], next_cursor: "", total_records: 0, page_size: 50 });
  const [timelineGroup, setTimelineGroup] = useState("");
  const [timelineLevel, setTimelineLevel] = useState<"year" | "month" | "day">("year");
  const [timelineStart, setTimelineStart] = useState("");
  const [periods, setPeriods] = useState<TimelinePeriod[]>([]);
  const [watchlist, setWatchlist] = useState<WatchlistItem[]>([]);
  const [watchlistGroup, setWatchlistGroup] = useState("");
  const [watchlistInterval, setWatchlistInterval] = useState(24);

  useEffect(() => {
    if (!job || ["completed", "failed", "cancelled"].includes(job.state)) return;
    const timer = window.setInterval(() => request<Job>(`/jobs/${job.id}`).then(setJob).catch((reason: unknown) => setError(reason instanceof Error ? reason.message : "Could not refresh job")), 5_000);
    return () => window.clearInterval(timer);
  }, [job?.id, job?.state]);
  useEffect(() => { localStorage.setItem("usenet-locator.page-size", String(pageSize)); }, [pageSize]);
  useEffect(() => { localStorage.setItem("usenet-locator.theme", theme); }, [theme]);

  async function createJob(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); setBusy(true); setError("");
    try {
      const providerList = await request<{ endpoints: Endpoint[] }>("/providers");
      const primary = providerList.endpoints.find((item) => item.primary);
      if (!primary) throw new Error("No primary provider endpoint is configured");
      const created = await request<{ id: string }>("/jobs", { method: "POST", body: JSON.stringify({ newsgroup, endpoint: primary.id, start_date: startDate, end_date: endDate, margin_days: marginDays, scan_reason: scanReason, source_job_id: sourceJobID, transfer_limit_bytes: transferLimit === "" ? null : Number(transferLimit) }) });
      setJob(await request<Job>(`/jobs/${created.id}`));
    } catch (reason) { setError(reason instanceof Error ? reason.message : "Could not create job"); }
    finally { setBusy(false); }
  }

  async function command(action: "pause" | "resume" | "cancel") {
    if (!job) return;
    setBusy(true); setError("");
    try { await request(`/jobs/${job.id}/${action}`, { method: "POST" }); setJob(await request<Job>(`/jobs/${job.id}`)); }
    catch (reason) { setError(reason instanceof Error ? reason.message : "Could not update job"); }
    finally { setBusy(false); }
  }

  async function search(cursor = "") {
    setBusy(true); setError("");
    try {
      const query = new URLSearchParams({ subject: searchSubject, author: searchAuthor, newsgroup: searchGroup, include_unwanted: String(includeUnwanted), limit: String(pageSize) });
      if (cursor) query.set("cursor", cursor);
      const page = await request<SearchPage>(`/search?${query}`);
      setArticles(cursor ? [...articles, ...page.articles] : page.articles); setSearchTotal(page.total_records); setNextCursor(page.next_cursor);
    } catch (reason) { setError(reason instanceof Error ? reason.message : "Could not search headers"); }
    finally { setBusy(false); }
  }

  async function markSelected(unwanted: boolean) {
    if (selected.size === 0) return;
    setBusy(true); setError("");
    try {
      const articleIDs = [...selected];
      await request("/articles/unwanted", { method: "POST", body: JSON.stringify({ article_ids: articleIDs, unwanted }) });
      setArticles((current) => current.filter((article) => includeUnwanted || !unwanted || !selected.has(article.id)).map((article) => selected.has(article.id) ? { ...article, unwanted } : article));
      setSelected(new Set());
    } catch (reason) { setError(reason instanceof Error ? reason.message : "Could not update unwanted marks"); }
    finally { setBusy(false); }
  }

  function toggleSelected(id: number) {
    setSelected((current) => { const next = new Set(current); next.has(id) ? next.delete(id) : next.add(id); return next; });
  }

  async function loadDetail(id: number) {
    setBusy(true); setError("");
    try { setDetail(await request<ArticleDetail>(`/articles/${id}`)); setBodyText(""); }
    catch (reason) { setError(reason instanceof Error ? reason.message : "Could not load article detail"); }
    finally { setBusy(false); }
  }

  async function retrieveBody() {
    if (!detail) return;
    setBusy(true); setError("");
    try { const response = await request<{ text: string }>(`/articles/${detail.id}/body`, { method: "POST" }); setBodyText(response.text); }
    catch (reason) { setError(reason instanceof Error ? reason.message : "Could not retrieve article text"); }
    finally { setBusy(false); }
  }

  async function loadProviders() {
    setBusy(true); setError("");
    try { const response = await request<{ endpoints: Endpoint[] }>("/providers"); setEndpoints(response.endpoints); }
    catch (reason) { setError(reason instanceof Error ? reason.message : "Could not load providers"); }
    finally { setBusy(false); }
  }

  async function loadQualifications(endpointID: string) {
    setBusy(true); setError("");
    try { const response = await request<{ qualifications: Qualification[] }>(`/providers/${encodeURIComponent(endpointID)}/qualifications`); setQualifications(response.qualifications); }
    catch (reason) { setError(reason instanceof Error ? reason.message : "Could not load qualification history"); }
    finally { setBusy(false); }
  }

  async function loadStorage() {
    setBusy(true); setError("");
    try { const [groupResponse, coverageResponse] = await Promise.all([request<{ newsgroups: GroupCount[] }>("/newsgroups"), request<{ coverage: Coverage[] }>("/coverage")]); setGroups(groupResponse.newsgroups); setCoverage(coverageResponse.coverage); }
    catch (reason) { setError(reason instanceof Error ? reason.message : "Could not load storage coverage"); }
    finally { setBusy(false); }
  }

	async function loadRetention(group: string) {
		setBusy(true); setError(""); setRetentionGroup(group);
		try { const response = await request<{ observations: RetentionObservation[] }>(`/newsgroups/${encodeURIComponent(group)}/retention`); setRetention(response.observations); }
		catch (reason) { setError(reason instanceof Error ? reason.message : "Could not load retained history"); }
		finally { setBusy(false); }
	}

	async function probeRetention(group: string) {
		setBusy(true); setError(""); setRetentionGroup(group);
		try { const response = await request<{ observations: RetentionObservation[] }>(`/newsgroups/${encodeURIComponent(group)}/retention-probe`, { method: "POST" }); setRetention(response.observations); }
		catch (reason) { setError(reason instanceof Error ? reason.message : "Could not probe retained history"); }
		finally { setBusy(false); }
	}

	async function retrieveOldestHeaders() {
		if (!retentionGroup) return;
		setBusy(true); setError("");
		try { await request(`/newsgroups/${encodeURIComponent(retentionGroup)}/oldest-headers?limit=${oldestHeaderCount}`, { method: "POST" }); await loadRetention(retentionGroup); }
		catch (reason) { setError(reason instanceof Error ? reason.message : "Could not retrieve oldest headers"); }
		finally { setBusy(false); }
	}

	async function loadChronological(cursor = "") {
		if (!retentionGroup) return;
		setBusy(true); setError("");
		try { setChronological(await request<SearchPage>(`/newsgroups/${encodeURIComponent(retentionGroup)}/headers?limit=${pageSize}${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`)); }
		catch (reason) { setError(reason instanceof Error ? reason.message : "Could not load chronological headers"); }
		finally { setBusy(false); }
	}

  async function loadWatchlist() {
    setBusy(true); setError("");
    try { const response = await request<{ watchlist: WatchlistItem[] }>("/watchlist"); setWatchlist(response.watchlist); }
    catch (reason) { setError(reason instanceof Error ? reason.message : "Could not load watchlist"); }
    finally { setBusy(false); }
  }

  async function saveWatchlist(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); setBusy(true); setError("");
    try { await request<WatchlistItem>("/watchlist", { method: "POST", body: JSON.stringify({ newsgroup: watchlistGroup, interval_hours: watchlistInterval }) }); setWatchlistGroup(""); await loadWatchlist(); }
    catch (reason) { setError(reason instanceof Error ? reason.message : "Could not save watchlist entry"); }
    finally { setBusy(false); }
  }

  async function removeWatchlist(group: string) {
    setBusy(true); setError("");
    try { await request(`/watchlist/${encodeURIComponent(group)}`, { method: "DELETE" }); setWatchlist((items) => items.filter((item) => item.newsgroup !== group)); }
    catch (reason) { setError(reason instanceof Error ? reason.message : "Could not remove watchlist entry"); }
    finally { setBusy(false); }
  }

  async function loadTimeline(group = timelineGroup, level = timelineLevel, start = timelineStart) {
    if (!group) return;
    setBusy(true); setError("");
    try { const query = new URLSearchParams({ level }); if (start) query.set("start_date", start); setTimelineGroup(group); setTimelineLevel(level); setTimelineStart(start); setPeriods((await request<{ periods: TimelinePeriod[] }>(`/newsgroups/${encodeURIComponent(group)}/timeline?${query}`)).periods); }
    catch (reason) { setError(reason instanceof Error ? reason.message : "Could not load timeline"); }
    finally { setBusy(false); }
  }
  async function completePeriod(period: TimelinePeriod) {
    if (!timelineGroup) return; setBusy(true); setError("");
    try { const outcome = await request<{ job_id?: string }>(`/newsgroups/${encodeURIComponent(timelineGroup)}/timeline/complete`, { method: "POST", body: JSON.stringify({ start_date: period.start_date, end_date: period.end_date }) }); if (outcome.job_id) setJob(await request<Job>(`/jobs/${outcome.job_id}`)); await loadTimeline(); }
    catch (reason) { setError(reason instanceof Error ? reason.message : "Could not queue coverage completion"); }
    finally { setBusy(false); }
  }
  async function goToPeriod(period: TimelinePeriod) {
    if (!timelineGroup) return; setBusy(true); setError("");
    try { const query = new URLSearchParams({ limit: String(pageSize), start_date: period.start_date, end_date: period.end_date }); const page = await request<SearchPage>(`/newsgroups/${encodeURIComponent(timelineGroup)}/headers?${query}`); setChronological(page); setRetentionGroup(timelineGroup); }
    catch (reason) { setError(reason instanceof Error ? reason.message : "Could not open chronological articles"); }
    finally { setBusy(false); }
  }

  const providerStateLabel = (state: JobState) => state.replace("_", " ");

  return <main className="app-shell" data-theme={theme} data-view={activeView}>
    <a className="skip-link" href="#main-content">Skip to content</a>
    <header className="topbar">
      <div><p className="eyebrow">Research console</p><h1>{applicationName}</h1></div>
      <nav aria-label="Primary navigation"><button className={activeView === "index" ? "active" : ""} aria-current={activeView === "index" ? "page" : undefined} onClick={() => setActiveView("index")}>Index</button><button className={activeView === "search" ? "active" : ""} aria-current={activeView === "search" ? "page" : undefined} onClick={() => setActiveView("search")}>Search</button><button className={activeView === "coverage" ? "active" : ""} aria-current={activeView === "coverage" ? "page" : undefined} onClick={() => setActiveView("coverage")}>Coverage</button><button className={activeView === "watchlist" ? "active" : ""} aria-current={activeView === "watchlist" ? "page" : undefined} onClick={() => setActiveView("watchlist")}>Watchlist</button></nav>
      <div className="toolbar"><label>Theme <select aria-label="Theme" value={theme} onChange={(event) => setTheme(event.target.value as Theme)}><option value="system">System</option><option value="light">Light</option><option value="dark">Dark</option></select></label><button className={activeView === "settings" ? "active" : ""} onClick={() => setActiveView("settings")}>Settings</button></div>
    </header>
    <div id="main-content" className="workspace" tabIndex={-1}>
    <section className="view index-view" hidden={activeView !== "index"}>
      <div className="view-heading"><div><p className="eyebrow">Scan workspace</p><h2>Index historical headers</h2><p>Queue a historical header scan across all configured providers. Work continues independently of this page.</p></div></div>
    <form className="card form-grid" onSubmit={createJob} aria-label="Create indexing job">
      <label>Newsgroup <input required placeholder="e.g. alt.test" value={newsgroup} onChange={(e) => setNewsgroup(e.target.value)} /></label>{" "}<label>Start <input required type="date" value={startDate} onChange={(e) => setStartDate(e.target.value)} /></label>{" "}<label>End <input required type="date" value={endDate} onChange={(e) => setEndDate(e.target.value)} /></label>{" "}<label>Margin days <input required type="number" min="0" max="31" value={marginDays} onChange={(e) => setMarginDays(Number(e.target.value))} /></label>{" "}<label>Reason <select value={scanReason} onChange={(e) => setScanReason(e.target.value)}><option value="operator_requested">Operator requested</option><option value="missing_range">Missing range</option><option value="failed_batch">Failed batch</option></select></label>{" "}{scanReason !== "operator_requested" && <><label>Source job <input required value={sourceJobID} onChange={(e) => setSourceJobID(e.target.value)} /></label>{" "}<label>Budget (bytes) <input min="1" type="number" value={transferLimit} onChange={(e) => setTransferLimit(e.target.value)} /></label>{" "}</>}<button disabled={busy} type="submit">Queue job</button>
    </form>
    {error && activeView === "index" && <p className="alert" role="alert">{error}</p>}
    {job && <section className="card job-card" aria-live="polite"><div className="job-summary"><div><p className="eyebrow">Active scan</p><h2>{job.newsgroup}</h2><p><span className={`state ${job.state}`}>{providerStateLabel(job.state)}</span> · {job.scan_reason ?? "operator requested"}</p></div><p>{job.headers_retrieved.toLocaleString()} headers<br />{job.articles_stored.toLocaleString()} stored</p></div>{job.transfer_limit_bytes && <p>Transfer usage: {formatBytes(job.transfer_used_bytes ?? 0)} / {formatBytes(job.transfer_limit_bytes)}</p>}{job.provider_jobs && <div className="provider-progress"><h3>Provider progress</h3>{job.provider_jobs.map((provider) => <div className="provider-row" key={provider.endpoint}><div><strong>{provider.endpoint}</strong><span className={`state ${provider.state}`}>{providerStateLabel(provider.state)}</span></div>{provider.state === "running" && <progress aria-label={`${provider.endpoint} progress`} />}{provider.state !== "running" && <div className="progress-track" aria-hidden="true" />}<small>{provider.headers_retrieved.toLocaleString()} headers · {provider.articles_stored.toLocaleString()} stored</small>{provider.last_error && <small className="error-text">{provider.last_error}</small>}</div>)}</div>}{job.last_error && <p className="alert">Last error: {job.last_error}</p>}<div className="actions">{job.state === "running" && <button disabled={busy} onClick={() => command("pause")}>Pause</button>}{["paused", "interrupted"].includes(job.state) && <button disabled={busy} onClick={() => command("resume")}>Resume</button>}{!["completed", "failed", "cancelled"].includes(job.state) && <button className="danger" disabled={busy} onClick={() => command("cancel")}>Cancel</button>}</div></section>}
    </section>
    <section className="view" hidden={activeView !== "search"}><div className="view-heading"><p className="eyebrow">Local archive</p><h2>Search stored headers</h2></div>{error && activeView === "search" && <p className="alert" role="alert">{error}</p>}<form className="card form-grid" onSubmit={(event) => { event.preventDefault(); void search(); }} aria-label="Search headers"><label>Subject <input value={searchSubject} onChange={(event) => setSearchSubject(event.target.value)} /></label>{" "}<label>Author <input value={searchAuthor} onChange={(event) => setSearchAuthor(event.target.value)} /></label>{" "}<label>Newsgroup <input value={searchGroup} onChange={(event) => setSearchGroup(event.target.value)} /></label>{" "}<label><input type="checkbox" checked={includeUnwanted} onChange={(event) => setIncludeUnwanted(event.target.checked)} /> Include unwanted</label>{" "}<label>Records per page <select value={pageSize} onChange={(event) => setPageSize(Number(event.target.value))}>{[10,25,50,100].map((value) => <option key={value} value={value}>{value}</option>)}</select></label>{" "}<button disabled={busy} type="submit">Search</button></form>
      {articles.length > 0 && <><p>{articles.length} shown of {searchTotal} total</p><p><button disabled={busy || selected.size === 0} onClick={() => void markSelected(true)}>Mark selected unwanted</button>{" "}<button disabled={busy || selected.size === 0} onClick={() => void markSelected(false)}>Clear unwanted mark</button></p><table><thead><tr><th>Select</th><th>Subject</th><th>Author</th><th>Date</th><th>Message-ID</th><th>Unwanted</th></tr></thead><tbody>{articles.map((article) => <tr key={article.id}><td><input aria-label={`Select ${article.message_id}`} type="checkbox" checked={selected.has(article.id)} onChange={() => toggleSelected(article.id)} /></td><td><button onClick={(event) => { detailReturnFocus.current = event.currentTarget; void loadDetail(article.id); }}>{article.subject || "(no subject)"}</button></td><td>{article.author}</td><td>{article.date ?? "Unknown"}</td><td>{article.message_id}</td><td>{article.unwanted ? "Yes" : "No"}</td></tr>)}</tbody></table></>}
      {nextCursor && <button disabled={busy} onClick={() => void search(nextCursor)}>Load more</button>}
      {detail && <aside className="card article-panel" role="dialog" aria-modal="false" aria-label="Article header"><div className="panel-title"><h2>Article header</h2><button className="close" onClick={() => { setDetail(null); setBodyText(""); detailReturnFocus.current?.focus(); }}>Close</button></div><p><strong>{detail.subject || "(no subject)"}</strong><br />From: {detail.author || "Unknown"}<br />Message-ID: {detail.message_id}<br />Newsgroups: {detail.newsgroups.join(", ") || "Unknown"}<br />Body cache: {detail.cached_body ? "available" : "not retrieved"}</p>{detail.unwanted ? <p>This article is locally marked unwanted. Clear its unwanted mark before requesting any new body text.</p> : <button disabled={busy} onClick={() => void retrieveBody()}>{detail.cached_body ? "Open cached text" : "Retrieve text"}</button>}{bodyText && <><pre>{bodyText}</pre><a href={`/api/v1/articles/${detail.id}/body/download`}>Download text (.txt)</a></>}</aside>}
    </section>
    <section className="view" hidden={activeView !== "watchlist"}><div className="view-heading"><p className="eyebrow">Scheduled checks</p><h2>Retention watchlist</h2><p>Regular bounded checks record each provider’s earliest retained header and current group bounds.</p></div>{error && activeView === "watchlist" && <p className="alert" role="alert">{error}</p>}<form className="card form-grid" onSubmit={saveWatchlist} aria-label="Add watchlist group"><label>Newsgroup <input required placeholder="e.g. alt.test" value={watchlistGroup} onChange={(event) => setWatchlistGroup(event.target.value)} /></label>{" "}<label>Check every <input required type="number" min="1" max="168" value={watchlistInterval} onChange={(event) => setWatchlistInterval(Number(event.target.value))} /> hours</label>{" "}<button disabled={busy} type="submit">Add to watchlist</button>{" "}<button disabled={busy} type="button" onClick={() => void loadWatchlist()}>Show watchlist</button></form>{watchlist.length > 0 && <table><thead><tr><th>Newsgroup</th><th>Interval</th><th>Last checked</th><th>Next check</th><th></th></tr></thead><tbody>{watchlist.map((item) => <tr key={item.newsgroup}><td><button disabled={busy} onClick={() => void loadRetention(item.newsgroup)}>{item.newsgroup}</button></td><td>{item.interval_hours}h</td><td>{item.last_checked_at ? new Date(item.last_checked_at).toLocaleString() : "Not yet"}</td><td>{new Date(item.next_check_at).toLocaleString()}</td><td><button disabled={busy} onClick={() => void removeWatchlist(item.newsgroup)}>Remove</button></td></tr>)}</tbody></table>}</section>
    <section className="view" hidden={activeView !== "settings"}><div className="view-heading"><p className="eyebrow">Configuration</p><h2>Providers</h2><p>Connection and quota details are available here and omitted from the routine indexing workspace.</p></div>{error && activeView === "settings" && <p className="alert" role="alert">{error}</p>}<button disabled={busy} onClick={() => void loadProviders()}>Show configured endpoints</button>{endpoints.length > 0 && <table><thead><tr><th>Endpoint</th><th>Host</th><th>TLS</th><th>Connections</th><th>Transfer usage</th><th>Primary</th><th>Priority</th><th>Qualification</th></tr></thead><tbody>{endpoints.map((endpoint) => <tr key={endpoint.id}><td>{endpoint.id}</td><td>{endpoint.host}:{endpoint.port}</td><td>{endpoint.tls ? "Required" : "Plaintext"}</td><td>{endpoint.connection_in_use}/{endpoint.connection_limit}</td><td>{endpoint.transfer_limit_bytes === null ? `${formatBytes(endpoint.transfer_used_bytes)} (unlimited)` : `${formatBytes(endpoint.transfer_used_bytes)} / ${formatBytes(endpoint.transfer_limit_bytes)}`}</td><td>{endpoint.primary ? "Yes" : "No"}</td><td>{endpoint.priority}</td><td><button disabled={busy} onClick={() => void loadQualifications(endpoint.id)}>Show history</button></td></tr>)}</tbody></table>}{qualifications.length > 0 && <table><caption>Provider qualification history</caption><thead><tr><th>When</th><th>Overview</th><th>Parsed dates</th><th>Capabilities</th></tr></thead><tbody>{qualifications.map((item) => <tr key={`${item.endpoint}-${item.created_at}`}><td>{new Date(item.created_at).toLocaleString()}</td><td>{item.result.overview_code || "Not probed"}</td><td>{item.result.overview_dates}/{item.result.overview_rows}</td><td>{item.result.capabilities.join(", ") || "None"}</td></tr>)}</tbody></table>}</section>
    <section className="view" hidden={activeView !== "coverage"}><div className="view-heading"><p className="eyebrow">Research coverage</p><h2>Timeline coverage</h2></div>{error && activeView === "coverage" && <p className="alert" role="alert">{error}</p>}<form className="card form-grid" onSubmit={(event) => { event.preventDefault(); void loadTimeline(); }}><label>Newsgroup <input required value={timelineGroup} onChange={(event) => setTimelineGroup(event.target.value)} /></label>{" "}<label>Level <select value={timelineLevel} onChange={(event) => setTimelineLevel(event.target.value as "year" | "month" | "day")}><option value="year">Years</option><option value="month">Months</option><option value="day">Days</option></select></label>{" "}<label>Parent/date jump <input type="date" value={timelineStart} onChange={(event) => setTimelineStart(event.target.value)} /></label>{" "}<button disabled={busy}>Browse</button></form>{periods.length > 0 && <table><thead><tr><th>Period</th><th>Articles</th><th>Coverage</th><th>Actions</th></tr></thead><tbody>{periods.map((period) => <tr key={`${period.level}-${period.start_date}`}><td>{period.start_date}–{period.end_date}</td><td>{period.article_count}</td><td title={period.endpoints.map((endpoint) => `${endpoint.endpoint}: ${endpoint.state}`).join(", ")}>{period.state}</td><td>{period.level !== "day" && <><button disabled={busy} onClick={() => void loadTimeline(timelineGroup, period.level === "year" ? "month" : "day", period.start_date)}>Zoom</button>{" "}</>}<button disabled={busy || period.state === "complete" || period.state === "pending"} onClick={() => void completePeriod(period)}>Complete coverage</button>{" "}<button disabled={busy} onClick={() => void goToPeriod(period)}>Go to articles</button></td></tr>)}</tbody></table>}</section>
    <section className="view" hidden={activeView !== "coverage"}><h2>Storage and coverage</h2><button disabled={busy} onClick={() => void loadStorage()}>Show stored groups and coverage</button>{groups.length > 0 && <table><caption>Stored groups</caption><thead><tr><th>Newsgroup</th><th>Articles</th><th>Retained history</th></tr></thead><tbody>{groups.map((group) => <tr key={group.name}><td><button disabled={busy} onClick={() => void loadRetention(group.name)}>{group.name}</button></td><td>{group.articles}</td><td><button disabled={busy} onClick={() => void probeRetention(group.name)}>Find earliest</button></td></tr>)}</tbody></table>}{retentionGroup && <section className="card"><h3>Earliest observed: {retentionGroup}</h3>{retention.map((item) => <p key={item.endpoint}>{item.article_id ? <button disabled={busy} onClick={() => void loadDetail(item.article_id!)}>Open earliest</button> : "Unavailable"} {item.observed_date ?? "Unknown"}</p>)}<p><button disabled={busy} onClick={() => void retrieveOldestHeaders()}>Retrieve oldest headers</button>{" "}<button disabled={busy} onClick={() => void loadChronological()}>Browse chronologically</button></p><h3>Articles: {retentionGroup} ({chronological.total_records} total)</h3>{chronological.articles.length > 0 && <><table><caption>Merged stored headers, oldest first</caption><thead><tr><th>Subject</th><th>Date</th></tr></thead><tbody>{chronological.articles.map((article) => <tr key={article.id}><td><button disabled={busy} onClick={() => void loadDetail(article.id)}>{article.subject || "(no subject)"}</button></td><td>{article.date}</td></tr>)}</tbody></table>{chronological.next_cursor && <button disabled={busy} onClick={() => void loadChronological(chronological.next_cursor)}>Newer headers</button>}</>}</section>}{coverage.length > 0 && <table><thead><tr><th>Newsgroup</th><th>Endpoint</th><th>Article range</th><th>State</th></tr></thead><tbody>{coverage.map((item) => <tr key={`${item.newsgroup}-${item.endpoint}-${item.article_number_start}`}><td>{item.newsgroup}</td><td>{item.endpoint}</td><td>{item.article_number_start}–{item.article_number_end}</td><td>{item.state}</td></tr>)}</tbody></table>}</section>
    </div>
  </main>;
}
