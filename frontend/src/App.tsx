import { FormEvent, useEffect, useState } from "react";
import { applicationName } from "./app-info";

type JobState = "queued" | "running" | "paused" | "interrupted" | "completed" | "failed" | "cancelled";
type Job = { id: string; newsgroup: string; endpoint: string; state: JobState; headers_retrieved: number; articles_stored: number; last_error?: string | null };
type Article = { id: number; message_id: string; subject: string; author: string; date?: string | null; unwanted: boolean };
type SearchPage = { articles: Article[]; next_cursor: string };
type ArticleDetail = Article & { references: string; bytes?: number | null; lines?: number | null; newsgroups: string[]; cached_body: boolean };
type Endpoint = { id: string; account_id: string; host: string; port: number; tls: boolean; primary: boolean; priority: number; connection_in_use: number; connection_limit: number; transfer_used_bytes: number; transfer_limit_bytes: number | null };
type GroupCount = { name: string; articles: number };
type Coverage = { endpoint: string; newsgroup: string; state: string; article_number_start: number; article_number_end: number };
type Qualification = { endpoint: string; result: { capabilities: string[]; overview_format_code: number; overview_fields: string[]; overview_code: number; overview_rows: number; overview_dates: number }; created_at: string };
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
  return response.json() as Promise<T>;
}

export function App() {
  const [newsgroup, setNewsgroup] = useState("comp.lang.go");
  const [endpoint, setEndpoint] = useState("primary");
  const [startDate, setStartDate] = useState("");
  const [endDate, setEndDate] = useState("");
  const [marginDays, setMarginDays] = useState(2);
  const [job, setJob] = useState<Job | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [searchSubject, setSearchSubject] = useState("");
  const [searchAuthor, setSearchAuthor] = useState("");
  const [searchGroup, setSearchGroup] = useState("");
  const [includeUnwanted, setIncludeUnwanted] = useState(false);
  const [articles, setArticles] = useState<Article[]>([]);
  const [nextCursor, setNextCursor] = useState("");
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const [detail, setDetail] = useState<ArticleDetail | null>(null);
  const [bodyText, setBodyText] = useState("");
  const [endpoints, setEndpoints] = useState<Endpoint[]>([]);
  const [groups, setGroups] = useState<GroupCount[]>([]);
  const [coverage, setCoverage] = useState<Coverage[]>([]);
  const [qualifications, setQualifications] = useState<Qualification[]>([]);

  useEffect(() => {
    if (!job || ["completed", "failed", "cancelled"].includes(job.state)) return;
    const timer = window.setInterval(() => request<Job>(`/jobs/${job.id}`).then(setJob).catch((reason: unknown) => setError(reason instanceof Error ? reason.message : "Could not refresh job")), 5_000);
    return () => window.clearInterval(timer);
  }, [job?.id, job?.state]);

  async function createJob(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); setBusy(true); setError("");
    try {
      const created = await request<{ id: string }>("/jobs", { method: "POST", body: JSON.stringify({ newsgroup, endpoint, start_date: startDate, end_date: endDate, margin_days: marginDays }) });
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
      const query = new URLSearchParams({ subject: searchSubject, author: searchAuthor, newsgroup: searchGroup, include_unwanted: String(includeUnwanted), limit: "50" });
      if (cursor) query.set("cursor", cursor);
      const page = await request<SearchPage>(`/search?${query}`);
      setArticles(cursor ? [...articles, ...page.articles] : page.articles); setNextCursor(page.next_cursor);
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

  return <main style={{ fontFamily: "system-ui, sans-serif", lineHeight: 1.5, margin: "2rem auto", maxWidth: 760 }}>
    <h1>{applicationName}</h1><p>Queue an endpoint-specific historical header scan. Work continues independently of this page.</p>
    <form onSubmit={createJob} aria-label="Create indexing job">
      <label>Newsgroup <input required value={newsgroup} onChange={(e) => setNewsgroup(e.target.value)} /></label>{" "}<label>Endpoint <input required value={endpoint} onChange={(e) => setEndpoint(e.target.value)} /></label>{" "}<label>Start <input required type="date" value={startDate} onChange={(e) => setStartDate(e.target.value)} /></label>{" "}<label>End <input required type="date" value={endDate} onChange={(e) => setEndDate(e.target.value)} /></label>{" "}<label>Margin days <input required type="number" min="0" max="31" value={marginDays} onChange={(e) => setMarginDays(Number(e.target.value))} /></label>{" "}<button disabled={busy} type="submit">Queue job</button>
    </form>
    {error && <p role="alert">{error}</p>}
    {job && <section aria-live="polite"><h2>Job status</h2><dl><dt>Newsgroup</dt><dd>{job.newsgroup}</dd><dt>State</dt><dd>{job.state}</dd><dt>Headers retrieved</dt><dd>{job.headers_retrieved}</dd><dt>Articles stored</dt><dd>{job.articles_stored}</dd></dl>{job.last_error && <p>Last error: {job.last_error}</p>}{job.state === "running" && <button disabled={busy} onClick={() => command("pause")}>Pause</button>}{" "}{["paused", "interrupted"].includes(job.state) && <button disabled={busy} onClick={() => command("resume")}>Resume</button>}{" "}{!["completed", "failed", "cancelled"].includes(job.state) && <button disabled={busy} onClick={() => command("cancel")}>Cancel</button>}</section>}
    <section><h2>Search stored headers</h2><form onSubmit={(event) => { event.preventDefault(); void search(); }} aria-label="Search headers"><label>Subject <input value={searchSubject} onChange={(event) => setSearchSubject(event.target.value)} /></label>{" "}<label>Author <input value={searchAuthor} onChange={(event) => setSearchAuthor(event.target.value)} /></label>{" "}<label>Newsgroup <input value={searchGroup} onChange={(event) => setSearchGroup(event.target.value)} /></label>{" "}<label><input type="checkbox" checked={includeUnwanted} onChange={(event) => setIncludeUnwanted(event.target.checked)} /> Include unwanted</label>{" "}<button disabled={busy} type="submit">Search</button></form>
      {articles.length > 0 && <><p><button disabled={busy || selected.size === 0} onClick={() => void markSelected(true)}>Mark selected unwanted</button>{" "}<button disabled={busy || selected.size === 0} onClick={() => void markSelected(false)}>Clear unwanted mark</button></p><table><thead><tr><th>Select</th><th>Subject</th><th>Author</th><th>Date</th><th>Message-ID</th><th>Unwanted</th></tr></thead><tbody>{articles.map((article) => <tr key={article.id}><td><input aria-label={`Select ${article.message_id}`} type="checkbox" checked={selected.has(article.id)} onChange={() => toggleSelected(article.id)} /></td><td><button onClick={() => void loadDetail(article.id)}>{article.subject || "(no subject)"}</button></td><td>{article.author}</td><td>{article.date ?? "Unknown"}</td><td>{article.message_id}</td><td>{article.unwanted ? "Yes" : "No"}</td></tr>)}</tbody></table></>}
      {nextCursor && <button disabled={busy} onClick={() => void search(nextCursor)}>Load more</button>}
    </section>
    {detail && <section><h2>Article header</h2><p><strong>{detail.subject || "(no subject)"}</strong><br />From: {detail.author || "Unknown"}<br />Message-ID: {detail.message_id}<br />Newsgroups: {detail.newsgroups.join(", ") || "Unknown"}<br />Body cache: {detail.cached_body ? "available" : "not retrieved"}</p>{detail.unwanted ? <p>This article is locally marked unwanted. Clear its unwanted mark before requesting any new body text.</p> : <button disabled={busy} onClick={() => void retrieveBody()}>{detail.cached_body ? "Open cached text" : "Retrieve text"}</button>}{bodyText && <><pre style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>{bodyText}</pre><a download="article.txt" href={`data:text/plain;charset=utf-8,${encodeURIComponent(bodyText)}`}>Download text (.txt)</a></>}</section>}
    <section><h2>Providers</h2><button disabled={busy} onClick={() => void loadProviders()}>Show configured endpoints</button>{endpoints.length > 0 && <table><thead><tr><th>Endpoint</th><th>Host</th><th>TLS</th><th>Connections</th><th>Transfer usage</th><th>Primary</th><th>Priority</th><th>Qualification</th></tr></thead><tbody>{endpoints.map((endpoint) => <tr key={endpoint.id}><td>{endpoint.id}</td><td>{endpoint.host}:{endpoint.port}</td><td>{endpoint.tls ? "Required" : "Plaintext"}</td><td>{endpoint.connection_in_use}/{endpoint.connection_limit}</td><td>{endpoint.transfer_limit_bytes === null ? `${formatBytes(endpoint.transfer_used_bytes)} (unlimited)` : `${formatBytes(endpoint.transfer_used_bytes)} / ${formatBytes(endpoint.transfer_limit_bytes)}`}</td><td>{endpoint.primary ? "Yes" : "No"}</td><td>{endpoint.priority}</td><td><button disabled={busy} onClick={() => void loadQualifications(endpoint.id)}>Show history</button></td></tr>)}</tbody></table>}{qualifications.length > 0 && <table><caption>Provider qualification history</caption><thead><tr><th>When</th><th>Overview</th><th>Parsed dates</th><th>Capabilities</th></tr></thead><tbody>{qualifications.map((item) => <tr key={`${item.endpoint}-${item.created_at}`}><td>{new Date(item.created_at).toLocaleString()}</td><td>{item.result.overview_code || "Not probed"}</td><td>{item.result.overview_dates}/{item.result.overview_rows}</td><td>{item.result.capabilities.join(", ") || "None"}</td></tr>)}</tbody></table>}</section>
    <section><h2>Storage and coverage</h2><button disabled={busy} onClick={() => void loadStorage()}>Show stored groups and coverage</button>{groups.length > 0 && <p>Stored groups: {groups.map((group) => `${group.name} (${group.articles})`).join(", ")}</p>}{coverage.length > 0 && <table><thead><tr><th>Newsgroup</th><th>Endpoint</th><th>Article range</th><th>State</th></tr></thead><tbody>{coverage.map((item) => <tr key={`${item.newsgroup}-${item.endpoint}-${item.article_number_start}`}><td>{item.newsgroup}</td><td>{item.endpoint}</td><td>{item.article_number_start}–{item.article_number_end}</td><td>{item.state}</td></tr>)}</tbody></table>}</section>
  </main>;
}
