import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { App } from "./App";

let root: ReturnType<typeof createRoot>;

beforeEach(() => {
  document.body.innerHTML = '<div id="root"></div>';
  root = createRoot(document.getElementById("root")!);
});
afterEach(() => { act(() => root.unmount()); localStorage.clear(); vi.unstubAllGlobals(); });

function setValue(element: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set;
  setter?.call(element, value);
  element.dispatchEvent(new Event("input", { bubbles: true }));
}
async function click(label: string) {
  const button = [...document.querySelectorAll("button")].find((element) => element.closest("nav") === null && element.textContent === label) as HTMLButtonElement;
  await act(async () => { button.click(); });
}

test("search form calls the unwanted-filtered API", async () => {
  const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ articles: [], next_cursor: "" }), { status: 200, headers: { "Content-Type": "application/json" } }));
  vi.stubGlobal("fetch", fetchMock);
  await act(async () => { root.render(<App />); });
  const button = [...document.querySelectorAll("button")].find((element) => element.closest("nav") === null && element.textContent === "Search") as HTMLButtonElement;
  await act(async () => { button.click(); });
  expect(fetchMock).toHaveBeenCalledWith("/api/v1/search?subject=&author=&newsgroup=&include_unwanted=false&limit=50", expect.any(Object));
});

test("archive evidence is local, reprocessing is explicit, and verification pages findings", async () => {
  const calls: string[] = [];
  const fetchMock = vi.fn(async (input: string | URL, options?: RequestInit) => {
    const url = String(input); calls.push(`${options?.method ?? "GET"} ${url}`);
    if (url.startsWith("/api/v1/search")) return new Response(JSON.stringify({ articles: [{ id: 1, message_id: "<one@test>", subject: "Example", author: "Alice", unwanted: false }], next_cursor: "", total_records: 1 }), { status: 200 });
    if (url === "/api/v1/articles/1") return new Response(JSON.stringify({ id: 1, message_id: "<one@test>", subject: "Example", author: "Alice", unwanted: false, newsgroups: ["comp.test"], cached_body: false, quality_flags: ["invalid_date"], normalizer_version: 1, source_overview: { subject: "RXhhbXBsZQ==" } }), { status: 200 });
    if (url === "/api/v1/articles/1/reprocess") return new Response(null, { status: 204 });
    if (url === "/api/v1/articles/1/body") return new Response(JSON.stringify({ text: "Hello\n> old reply" }), { status: 200 });
    if (url === "/api/v1/articles/1/reader") return new Response(JSON.stringify({ text: "Hello\n> old reply", html: "<p>safe</p>", raw_headers: "Content-Type: text/plain", flags: [], parts: [{ content_type: "text/plain" }] }), { status: 200 });
    if (url.startsWith("/api/v1/archive/verification")) return new Response(JSON.stringify({ category: "membership_inconsistencies", findings: [{ key: url.includes("after=") ? "2" : "1", description: "Review membership" }], ...(url.includes("after=") ? {} : { next_cursor: "1" }) }), { status: 200 });
    if (url === "/api/v1/jobs?limit=50") return new Response(JSON.stringify({ jobs: [] }), { status: 200 });
    return new Response("{}", { status: 500 });
  });
  vi.stubGlobal("fetch", fetchMock);
  await act(async () => { root.render(<App />); });
  await click("Search"); await click("Example");
  expect(calls.some((call) => call.includes("/body"))).toBe(false);
  expect(document.body.textContent).toContain("invalid_date");
  await click("Reprocess stored source");
  expect(calls).toContain("POST /api/v1/articles/1/reprocess");
  await click("Retrieve text");
  expect(document.body.textContent).toContain("Quoted text");
  expect(document.body.textContent).toContain("Content-Type: text/plain");
  await click("Run verification");
  expect(document.body.textContent).toContain("Review membership (record 1)");
  await click("Next findings");
  expect(document.body.textContent).toContain("Review membership (record 2)");
});

test("navigation isolates provider settings and persists the selected theme", async () => {
  await act(async () => { root.render(<App />); });
  const settings = [...document.querySelectorAll("nav + .toolbar button")].find((element) => element.textContent === "Settings") as HTMLButtonElement;
  await act(async () => { settings.click(); });
  expect([...document.querySelectorAll("h2")].find((heading) => heading.textContent === "Providers")?.closest("section")?.hidden).toBe(false);
  const theme = document.querySelector('select[aria-label="Theme"]') as HTMLSelectElement;
  const selectSetter = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, "value")?.set;
  await act(async () => { selectSetter?.call(theme, "dark"); theme.dispatchEvent(new Event("change", { bubbles: true })); });
  expect(localStorage.getItem("usenet-locator.theme")).toBe("dark");
});

test("job controls, result marks, reader retrieval, and providers use the API", async () => {
  let jobState = "running";
	let searchCalls = 0;
  const fetchMock = vi.fn(async (input: string | URL, options?: RequestInit) => {
    const url = String(input); const method = options?.method ?? "GET";
    if (url === "/api/v1/jobs" && method === "POST") return new Response(JSON.stringify({ id: "job-1" }), { status: 201 });
    if (url === "/api/v1/jobs/job-1/pause") { jobState = "paused"; return new Response("{}", { status: 200 }); }
    if (url === "/api/v1/jobs/job-1/resume") { jobState = "queued"; return new Response("{}", { status: 200 }); }
    if (url === "/api/v1/jobs/job-1/cancel") { jobState = "cancelled"; return new Response("{}", { status: 200 }); }
    if (url === "/api/v1/jobs/job-1") return new Response(JSON.stringify({ id: "job-1", newsgroup: "comp.lang.go", endpoint: "primary", state: jobState, headers_retrieved: 1, articles_stored: 1 }), { status: 200 });
    if (url.startsWith("/api/v1/search")) { searchCalls++; return new Response(JSON.stringify({ articles: [{ id: 4, message_id: "<a@test>", subject: "A subject", author: "Alice", unwanted: false }], next_cursor: searchCalls === 1 ? "next-page" : "" }), { status: 200 }); }
    if (url === "/api/v1/articles/unwanted") return new Response("{}", { status: 200 });
    if (url === "/api/v1/articles/4") return new Response(JSON.stringify({ id: 4, message_id: "<a@test>", subject: "A subject", author: "Alice", newsgroups: ["comp.lang.go"], unwanted: false, cached_body: false }), { status: 200 });
    if (url === "/api/v1/articles/4/body") return new Response(JSON.stringify({ text: "body text" }), { status: 200 });
    if (url === "/api/v1/articles/4/reader") return new Response(JSON.stringify({ text: "body text", parts: [], flags: [] }), { status: 200 });
    if (url === "/api/v1/providers") return new Response(JSON.stringify({ endpoints: [{ id: "primary", host: "news.example", port: 563, tls: true, primary: true, priority: 1 }] }), { status: 200 });
    if (url === "/api/v1/providers/primary/qualifications") return new Response(JSON.stringify({ qualifications: [{ endpoint: "primary", created_at: "2026-09-30T00:00:00Z", result: { capabilities: ["READER"], overview_code: 224, overview_rows: 1, overview_dates: 1 } }] }), { status: 200 });
    if (url === "/api/v1/newsgroups") return new Response(JSON.stringify({ newsgroups: [{ name: "comp.lang.go", articles: 2 }] }), { status: 200 });
    if (url === "/api/v1/coverage") return new Response(JSON.stringify({ coverage: [{ endpoint: "primary", newsgroup: "comp.lang.go", state: "complete", article_number_start: 1, article_number_end: 2 }] }), { status: 200 });
    if (url === "/api/v1/discovery-jobs" && method === "POST") return new Response(JSON.stringify({ id: "discovery-1", state: "queued" }), { status: 202 });
    if (url === "/api/v1/jobs/discovery-1") return new Response(JSON.stringify({ id: "discovery-1", kind: "retention_discovery", newsgroup: "comp.lang.go", state: "queued", headers_retrieved: 0, articles_stored: 0, provider_jobs: [] }), { status: 200 });
    if (url === "/api/v1/jobs?limit=50") return new Response(JSON.stringify({ jobs: [] }), { status: 200 });
		if (url === "/api/v1/newsgroups/comp.lang.go/retention") return new Response(JSON.stringify({ observations: [{ endpoint: "primary", newsgroup: "comp.lang.go", article_id: 4, observed_date: "2000-01-01T00:00:00Z", outcome: "found", observed_at: "2026-10-01T00:00:00Z" }] }), { status: 200 });
		if (url === "/api/v1/newsgroups/comp.lang.go/oldest-headers?limit=10") return new Response(JSON.stringify({ newsgroup: "comp.lang.go", limit: 10 }), { status: 202 });
    if (url.startsWith("/api/v1/newsgroups/comp.lang.go/headers?")) return new Response(JSON.stringify({ articles: [], next_cursor: "", total_records: 0, page_size: 50 }), { status: 200 });
    return new Response("{}", { status: 500 });
  });
  vi.stubGlobal("fetch", fetchMock);
  await act(async () => { root.render(<App />); });
  const dates = document.querySelectorAll('input[type="date"]') as NodeListOf<HTMLInputElement>;
  const newsgroup = document.querySelector('input[placeholder="e.g. alt.test"]') as HTMLInputElement;
  await act(async () => { setValue(newsgroup, "comp.lang.go"); setValue(dates[0], "2020-01-01"); setValue(dates[1], "2020-01-02"); });
  await click("Queue job"); await click("Pause"); await click("Resume"); await click("Cancel");
  await click("Search"); await click("Load more");
  await act(async () => { (document.querySelector('input[aria-label="Select <a@test>"]') as HTMLInputElement).click(); });
  await click("Mark selected unwanted"); await click("Search"); await click("A subject"); await click("Retrieve text"); await click("Show configured endpoints"); await click("Show history"); await click("Show stored groups and coverage"); await click("comp.lang.go"); await click("Find earliest"); await click("Retrieve oldest headers"); await click("Browse chronologically");
  expect(document.body.textContent).toContain("body text");
  expect(document.querySelector('[aria-label="Article header"]')?.textContent).toContain("Date: Unknown");
  expect(document.body.textContent).toContain("news.example:563");
  expect(document.body.textContent).toContain("Earliest observed: comp.lang.go");
	expect(document.body.textContent).toContain("Open earliest");
	expect(document.body.textContent).toContain("Provider qualification history");
});

test("Coverage queues a new group and displays provider progress and completion", async () => {
  vi.useFakeTimers();
  try {
    let state: "queued" | "completed" = "queued";
    const job = () => ({ id: "discovery-new", kind: "retention_discovery", newsgroup: "alt.new", state, headers_retrieved: state === "completed" ? 1 : 0, articles_stored: state === "completed" ? 1 : 0, created_at: "2026-10-01T00:00:00Z", provider_jobs: [{ endpoint: "primary", state, headers_retrieved: state === "completed" ? 1 : 0, articles_stored: state === "completed" ? 1 : 0, transfer_used_bytes: 42, ...(state === "completed" ? { outcome: "found", group_low: 10, group_high: 90, article_number: 11 } : {}) }] });
    const fetchMock = vi.fn(async (input: string | URL, options?: RequestInit) => {
      const url = String(input);
      if (url === "/api/v1/jobs?limit=50") return new Response(JSON.stringify({ jobs: state === "completed" ? [job()] : [] }), { status: 200 });
      if (url === "/api/v1/discovery-jobs" && options?.method === "POST") return new Response(JSON.stringify({ id: "discovery-new", state: "queued" }), { status: 202 });
      if (url === "/api/v1/jobs/discovery-new") return new Response(JSON.stringify(job()), { status: 200 });
      if (url === "/api/v1/newsgroups/alt.new/retention") return new Response(JSON.stringify({ observations: [{ endpoint: "primary", newsgroup: "alt.new", outcome: "found", article_id: 1, group_low: 10, group_high: 90, observed_at: "2026-10-01T00:00:00Z", next_article_number: 12, cursor_high: 90 }] }), { status: 200 });
      if (url.startsWith("/api/v1/newsgroups/alt.new/headers?")) return new Response(JSON.stringify({ articles: [], next_cursor: "", total_records: 1, page_size: 50 }), { status: 200 });
      return new Response("{}", { status: 404 });
    });
    vi.stubGlobal("fetch", fetchMock);
    await act(async () => { root.render(<App />); });
    const coverage = [...document.querySelectorAll<HTMLButtonElement>("nav button")].find((button) => button.textContent === "Coverage")!;
    await act(async () => { coverage.click(); });
    const input = document.querySelector('form[aria-label="Discover earliest headers"] input') as HTMLInputElement;
    await act(async () => { setValue(input, "alt.new"); });
    await click("Discover earliest headers");
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/discovery-jobs", expect.objectContaining({ method: "POST", body: JSON.stringify({ newsgroup: "alt.new" }) }));
    expect(document.querySelector('[aria-label="Job queue"]')?.textContent).toContain("primary");
    expect(document.body.textContent).toContain("Discovery for alt.new is queued.");
    state = "completed";
    await act(async () => { await vi.advanceTimersByTimeAsync(5_000); });
    expect(document.querySelector('[aria-label="Job queue"]')?.textContent).toContain("group bounds 10–90");
    expect(document.body.textContent).toContain("Open earliest");
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/newsgroups/alt.new/retention", expect.any(Object));
  } finally { vi.useRealTimers(); }
});

test("Coverage shows existing jobs and a partial provider failure", async () => {
  const item = { id: "partial", kind: "retention_discovery", newsgroup: "comp.existing", state: "failed", headers_retrieved: 1, articles_stored: 1, last_error: "1 of 2 provider scans did not complete", provider_jobs: [
    { endpoint: "healthy", state: "completed", headers_retrieved: 1, articles_stored: 1, outcome: "found", group_low: 10, group_high: 100 },
    { endpoint: "offline", state: "failed", headers_retrieved: 0, articles_stored: 0, outcome: "connection_failed", last_error: "connection_failed" },
  ] };
  vi.stubGlobal("fetch", vi.fn(async (input: string | URL) => String(input) === "/api/v1/jobs?limit=50" ? new Response(JSON.stringify({ jobs: [item] }), { status: 200 }) : new Response("{}", { status: 404 })));
  await act(async () => { root.render(<App />); });
  const coverage = [...document.querySelectorAll<HTMLButtonElement>("nav button")].find((button) => button.textContent === "Coverage")!;
  await act(async () => { coverage.click(); });
  const queue = document.querySelector('[aria-label="Job queue"]')!;
  expect(queue.textContent).toContain("comp.existing");
  expect(queue.textContent).toContain("healthy");
  expect(queue.textContent).toContain("offline");
  expect(queue.textContent).toContain("connection failed");
  expect(queue.textContent).toContain("Retry discovery");
});

test("watchlist can be displayed, saved, opened, and removed", async () => {
  let groups = [{ newsgroup: "alt.watch", interval_hours: 12, last_checked_at: null, next_check_at: "2026-10-03T00:00:00Z" }];
  const fetchMock = vi.fn(async (input: string | URL, options?: RequestInit) => {
    const url = String(input); const method = options?.method ?? "GET";
    if (url === "/api/v1/watchlist" && method === "GET") return new Response(JSON.stringify({ watchlist: groups }), { status: 200 });
    if (url === "/api/v1/watchlist" && method === "POST") { groups = [{ newsgroup: "alt.new", interval_hours: 24, last_checked_at: null, next_check_at: "2026-10-03T00:00:00Z" }]; return new Response(JSON.stringify(groups[0]), { status: 201 }); }
    if (url === "/api/v1/watchlist/alt.new" && method === "DELETE") return new Response(null, { status: 204 });
    if (url === "/api/v1/newsgroups/alt.watch/retention") return new Response(JSON.stringify({ observations: [] }), { status: 200 });
    return new Response(JSON.stringify({ watchlist: [] }), { status: 200 });
  });
  vi.stubGlobal("fetch", fetchMock);
  await act(async () => { root.render(<App />); });
  await click("Show watchlist");
  expect(document.body.textContent).toContain("alt.watch");
  await click("alt.watch");
  const watchInput = document.querySelector('form[aria-label="Add watchlist group"] input[placeholder="e.g. alt.test"]') as HTMLInputElement;
  await act(async () => { setValue(watchInput, "alt.new"); });
  await click("Add to watchlist");
  expect(document.body.textContent).toContain("alt.new");
  await click("Remove");
  expect(document.body.textContent).not.toContain("alt.new");
});

test("watchlist failures are shown to the operator", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ error: "unavailable" }), { status: 500 })));
  await act(async () => { root.render(<App />); });
  await click("Show watchlist");
  expect(document.body.textContent).toContain("unavailable");
});

test("timeline exposes gap completion and read-only chronological navigation", async () => {
  const fetchMock = vi.fn(async (input: string | URL, options?: RequestInit) => {
    const url = String(input);
    if (url.startsWith("/api/v1/newsgroups/comp.timeline/timeline?")) return new Response(JSON.stringify({ periods: [{ level: "year", start_date: "2020-01-01", end_date: "2020-12-31", article_count: 2, state: "gaps", endpoints: [{ endpoint: "primary", state: "gaps" }] }] }), { status: 200 });
    if (url === "/api/v1/newsgroups/comp.timeline/timeline/complete" && options?.method === "POST") return new Response(JSON.stringify({ state: "queued", job_id: "timeline-job" }), { status: 202 });
    if (url === "/api/v1/jobs/timeline-job") return new Response(JSON.stringify({ id: "timeline-job", newsgroup: "comp.timeline", endpoint: "primary", state: "queued", headers_retrieved: 0, articles_stored: 0 }), { status: 200 });
    if (url.startsWith("/api/v1/newsgroups/comp.timeline/headers?")) return new Response(JSON.stringify({ articles: [{ id: 1, message_id: "<timeline@test>", subject: "Timeline", author: "A", date: "2020-01-01T00:00:00Z", unwanted: false }], next_cursor: "", total_records: 1, page_size: 50 }), { status: 200 });
    return new Response(JSON.stringify({ periods: [] }), { status: 200 });
  });
  vi.stubGlobal("fetch", fetchMock);
  await act(async () => { root.render(<App />); });
  const timelineSection = [...document.querySelectorAll("section")].find((section) => section.querySelector("h2")?.textContent === "Timeline coverage")!;
  const timelineInput = timelineSection.querySelector("input") as HTMLInputElement;
  await act(async () => { setValue(timelineInput, "comp.timeline"); });
  await click("Browse");
  expect(document.body.textContent).toContain("gaps");
  await click("Complete coverage");
  await click("Go to articles");
  expect(document.body.textContent).toContain("Timeline");
});

test("search preserves submitted message and date filters across pages", async () => {
  const fetchMock = vi.fn(async (input: string | URL) => {
    const url = String(input);
    return new Response(JSON.stringify({ articles: [{ id: 1, message_id: "<one@test>", subject: "One", author: "A", unwanted: false }], next_cursor: url.includes("cursor=") ? "" : "next", total_records: 2, page_size: 50 }), { status: 200 });
  });
  vi.stubGlobal("fetch", fetchMock);
  await act(async () => { root.render(<App />); });
  const form = document.querySelector('form[aria-label="Search headers"]')!;
  const inputs = form.querySelectorAll("input");
  await act(async () => { setValue(inputs[3], "<one@test>"); setValue(inputs[4], "2020-01-01"); setValue(inputs[5], "2020-01-31"); });
  await click("Search");
  await act(async () => { setValue(inputs[3], "<changed@test>"); });
  await click("Load more");
  const lastURL = String(fetchMock.mock.calls.at(-1)?.[0]);
  const query = new URL(lastURL, "http://localhost").searchParams;
  expect(query.get("message_id")).toBe("<one@test>");
  expect(query.get("start_date")).toBe("2020-01-01");
  expect(query.get("end_date")).toBe("2020-01-31");
  expect(query.get("cursor")).toBe("next");
  await act(async () => { setValue(inputs[4], "2021-01-01"); });
  const calls = fetchMock.mock.calls.length;
  await click("Search");
  expect(fetchMock.mock.calls.length).toBe(calls);
  expect(document.body.textContent).toContain("Start date must be on or before end date");
});

test("timeline paging retains period and whole-group navigation removes it", async () => {
  const fetchMock = vi.fn(async (input: string | URL) => {
    const url = String(input);
    if (url.includes("/timeline?")) return new Response(JSON.stringify({ periods: [{ level: "year", start_date: "2020-01-01", end_date: "2020-12-31", article_count: 2, state: "gaps", endpoints: [] }] }), { status: 200 });
    if (url.includes("/headers?")) return new Response(JSON.stringify({ articles: [{ id: 1, subject: "First", author: "A", message_id: "<one@test>", unwanted: false }], next_cursor: url.includes("cursor=") ? "" : "next", total_records: 2, page_size: 50 }), { status: 200 });
    if (url.includes("/jobs?")) return new Response(JSON.stringify({ jobs: [] }), { status: 200 });
    return new Response(JSON.stringify({ observations: [] }), { status: 200 });
  });
  vi.stubGlobal("fetch", fetchMock);
  await act(async () => { root.render(<App />); });
  const section = [...document.querySelectorAll("section")].find((item) => item.querySelector("h2")?.textContent === "Timeline coverage")!;
  await act(async () => { setValue(section.querySelector("input")!, "comp.timeline"); });
  await click("Browse"); await click("Go to articles"); await click("Newer headers");
  let query = new URL(String(fetchMock.mock.calls.at(-1)?.[0]), "http://localhost").searchParams;
  expect(query.get("start_date")).toBe("2020-01-01"); expect(query.get("end_date")).toBe("2020-12-31"); expect(query.get("cursor")).toBe("next");
  await click("Browse whole group");
  query = new URL(String(fetchMock.mock.calls.at(-1)?.[0]), "http://localhost").searchParams;
  expect(query.has("start_date")).toBe(false); expect(query.has("cursor")).toBe(false);
});

test("provider qualification is explicit and refreshes safe history", async () => {
  const result = { capabilities: ["READER"], overview_format_code: 215, overview_fields: ["Subject"], stat_code: 223, group_selected: true, group_low: 1, group_high: 10, overview_code: 224, overview_rows: 1, overview_dates: 1 };
  const fetchMock = vi.fn(async (input: string | URL, options?: RequestInit) => {
    const url = String(input);
    if (url.endsWith("/providers")) return new Response(JSON.stringify({ endpoints: [{ id: "primary", enabled: true, host: "news.example", port: 563, tls: true, primary: true, priority: 1, connection_in_use: 0, connection_limit: 1, transfer_used_bytes: 0, transfer_limit_bytes: null }] }), { status: 200 });
    if (url.endsWith("/preflight") && options?.method === "POST") return new Response(JSON.stringify(result), { status: 200 });
    if (url.endsWith("/qualifications")) return new Response(JSON.stringify({ qualifications: [] }), { status: 200 });
    return new Response("{}", { status: 404 });
  });
  vi.stubGlobal("fetch", fetchMock);
  await act(async () => { root.render(<App />); });
  await click("Show configured endpoints"); await click("Show history");
  expect(fetchMock.mock.calls.some(([url]) => String(url).endsWith("/preflight"))).toBe(false);
  const form = document.querySelector('form[aria-label="Provider compatibility check"]')!;
  const inputs = form.querySelectorAll("input");
  await act(async () => { setValue(inputs[0], "<one@test>"); setValue(inputs[1], "alt.test"); setValue(inputs[2], "5"); });
  await click("Run compatibility check");
  expect(fetchMock).toHaveBeenCalledWith("/api/v1/providers/primary/preflight", expect.objectContaining({ method: "POST", body: JSON.stringify({ message_id: "<one@test>", newsgroup: "alt.test", article_number: 5 }) }));
  expect(fetchMock.mock.calls.filter(([url]) => String(url).endsWith("/qualifications"))).toHaveLength(2);
  expect(document.body.textContent).toContain("1–10");
});

test("queue can pause a running job and use it as the source of a scan", async () => {
  let state = "running";
  const job = () => ({ id: "job-1", kind: "index", newsgroup: "comp.test", endpoint: "primary", start_date: "2020-01-01", end_date: "2020-01-31", margin_days: 2, state, headers_retrieved: 2, articles_stored: 1, provider_jobs: [] });
  const fetchMock = vi.fn(async (input: string | URL, options?: RequestInit) => {
    const url = String(input);
    if (url.endsWith("/jobs?limit=50")) return new Response(JSON.stringify({ jobs: [job()] }), { status: 200 });
    if (url.endsWith("/jobs/job-1/pause") && options?.method === "POST") { state = "paused"; return new Response("{}", { status: 200 }); }
    if (url.endsWith("/jobs/job-1")) return new Response(JSON.stringify(job()), { status: 200 });
    return new Response("{}", { status: 404 });
  });
  vi.stubGlobal("fetch", fetchMock);
  await act(async () => { root.render(<App />); });
  await act(async () => { [...document.querySelectorAll<HTMLButtonElement>("nav button")].find((button) => button.textContent === "Coverage")!.click(); });
  expect(document.querySelector('[aria-label="Job queue"]')?.textContent).toContain("Job ID: job-1");
  await click("Pause job"); expect(document.body.textContent).toContain("Resume job");
  await click("Use as source");
  expect((document.querySelector('input[placeholder="e.g. alt.test"]') as HTMLInputElement).value).toBe("comp.test");
  expect([...document.querySelectorAll("input")].some((input) => input.value === "job-1")).toBe(true);
  const form = document.querySelector('form[aria-label="Create indexing job"]') as HTMLFormElement;
  const budget = [...form.querySelectorAll<HTMLInputElement>('input[type="number"]')].at(-1)!;
  await act(async () => { setValue(budget, "0"); });
  expect(form.checkValidity()).toBe(false);
  await act(async () => { setValue(budget, "100"); });
  expect(form.checkValidity()).toBe(true);
});

test("provider errors and disabled endpoints are visible without exposing a check action", async () => {
  const fetchMock = vi.fn(async (input: string | URL) => {
    const url = String(input);
    if (url.endsWith("/providers")) return new Response(JSON.stringify({ endpoints: [{ id: "offline", enabled: false, host: "news.example", port: 563, tls: true, primary: false, priority: 1, connection_in_use: 0, connection_limit: 1, transfer_used_bytes: 0, transfer_limit_bytes: null }] }), { status: 200 });
    if (url.endsWith("/qualifications")) return new Response(JSON.stringify({ qualifications: [] }), { status: 200 });
    return new Response(JSON.stringify({ error: "configured provider transfer quota has been reached" }), { status: 429 });
  });
  vi.stubGlobal("fetch", fetchMock);
  await act(async () => { root.render(<App />); });
  await click("Show configured endpoints"); await click("Show history");
  expect(document.body.textContent).toContain("Disabled");
  expect(([...document.querySelectorAll("button")].find((item) => item.textContent === "Run compatibility check") as HTMLButtonElement).disabled).toBe(true);
  expect(fetchMock.mock.calls.some(([url]) => String(url).endsWith("/preflight"))).toBe(false);
});

test("qualification quota failures are shown and do not refresh history", async () => {
  const fetchMock = vi.fn(async (input: string | URL) => {
    const url = String(input);
    if (url.endsWith("/providers")) return new Response(JSON.stringify({ endpoints: [{ id: "primary", enabled: true, host: "news.example", port: 563, tls: true, primary: true, priority: 1, connection_in_use: 0, connection_limit: 1, transfer_used_bytes: 0, transfer_limit_bytes: null }] }), { status: 200 });
    if (url.endsWith("/qualifications")) return new Response(JSON.stringify({ qualifications: [] }), { status: 200 });
    return new Response(JSON.stringify({ error: "configured provider transfer quota has been reached" }), { status: 429 });
  });
  vi.stubGlobal("fetch", fetchMock);
  await act(async () => { root.render(<App />); });
  await click("Show configured endpoints"); await click("Show history"); await click("Run compatibility check");
  expect(document.body.textContent).toContain("configured provider transfer quota has been reached");
  expect(fetchMock.mock.calls.filter(([url]) => String(url).endsWith("/qualifications"))).toHaveLength(1);
});

test("job lookup and conflicting commands preserve a refreshed state", async () => {
  let state = "running";
  const job = () => ({ id: "older", kind: "index", newsgroup: "comp.old", endpoint: "primary", state, headers_retrieved: 0, articles_stored: 0, provider_jobs: [] });
  const fetchMock = vi.fn(async (input: string | URL) => {
    const url = String(input);
    if (url.endsWith("/jobs?limit=50")) return new Response(JSON.stringify({ jobs: [] }), { status: 200 });
    if (url.endsWith("/jobs/older/pause")) { state = "interrupted"; return new Response(JSON.stringify({ error: "job state changed" }), { status: 409 }); }
    if (url.endsWith("/jobs/older")) return new Response(JSON.stringify(job()), { status: 200 });
    return new Response("{}", { status: 404 });
  });
  vi.stubGlobal("fetch", fetchMock);
  await act(async () => { root.render(<App />); });
  await act(async () => { [...document.querySelectorAll<HTMLButtonElement>("nav button")].find((button) => button.textContent === "Coverage")!.click(); });
  await act(async () => { setValue(document.querySelector('form[aria-label="Find an older job"] input')!, "older"); });
  await click("Find job"); await click("Pause job");
  expect(document.body.textContent).toContain("job state changed");
  expect(document.querySelector('[aria-label="Job queue"]')?.textContent).toContain("interrupted");
  expect(document.querySelector('[aria-label="Job queue"]')?.textContent).toContain("Resume job");
});

test("article and coverage views show stored evidence and unknown values", async () => {
  const fetchMock = vi.fn(async (input: string | URL) => {
    const url = String(input);
    if (url.includes("/search?")) return new Response(JSON.stringify({ articles: [{ id: 1, message_id: "<one@test>", subject: "One", author: "A", unwanted: false }], next_cursor: "", total_records: 1, page_size: 50 }), { status: 200 });
    if (url.endsWith("/articles/1")) return new Response(JSON.stringify({ id: 1, message_id: "<one@test>", subject: "One", author: "A", date: "2020-01-02T03:04:05Z", unwanted: false, references: "<parent@test>", bytes: 123, lines: null, newsgroups: ["alt.test"], cached_body: false }), { status: 200 });
    if (url.endsWith("/newsgroups")) return new Response(JSON.stringify({ newsgroups: [{ name: "alt.test", articles: 1 }] }), { status: 200 });
    if (url.endsWith("/coverage")) return new Response(JSON.stringify({ coverage: [{ endpoint: "primary", newsgroup: "alt.test", state: "partial", article_number_start: 1, article_number_end: 3, observed_start: "2020-01-01T00:00:00Z", observed_end: null, reason: "incomplete" }] }), { status: 200 });
    return new Response(JSON.stringify({ jobs: [] }), { status: 200 });
  });
  vi.stubGlobal("fetch", fetchMock);
  await act(async () => { root.render(<App />); });
  await click("Search"); await click("One");
  expect(document.querySelector('[aria-label="Article header"]')?.textContent).toContain("<parent@test>");
  expect(document.querySelector('[aria-label="Article header"]')?.textContent).toContain("Date: 2020-01-02T03:04:05Z");
  expect(document.querySelector('[aria-label="Article header"]')?.textContent).toContain("Bytes: 123");
  expect(document.querySelector('[aria-label="Article header"]')?.textContent).toContain("Lines: Unknown");
  await click("Show stored groups and coverage");
  expect(document.body.textContent).toContain("2020-01-01T00:00:00Z");
  expect(document.body.textContent).toContain("incomplete");
});
