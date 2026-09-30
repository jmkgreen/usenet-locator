import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { App } from "./App";

let root: ReturnType<typeof createRoot>;

beforeEach(() => {
  document.body.innerHTML = '<div id="root"></div>';
  root = createRoot(document.getElementById("root")!);
});
afterEach(() => { act(() => root.unmount()); vi.unstubAllGlobals(); });

function setValue(element: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set;
  setter?.call(element, value);
  element.dispatchEvent(new Event("input", { bubbles: true }));
}
async function click(label: string) {
  const button = [...document.querySelectorAll("button")].find((element) => element.textContent === label) as HTMLButtonElement;
  await act(async () => { button.click(); });
}

test("search form calls the unwanted-filtered API", async () => {
  const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ articles: [], next_cursor: "" }), { status: 200, headers: { "Content-Type": "application/json" } }));
  vi.stubGlobal("fetch", fetchMock);
  await act(async () => { root.render(<App />); });
  const button = [...document.querySelectorAll("button")].find((element) => element.textContent === "Search") as HTMLButtonElement;
  await act(async () => { button.click(); });
  expect(fetchMock).toHaveBeenCalledWith("/api/v1/search?subject=&author=&newsgroup=&include_unwanted=false&limit=50", expect.any(Object));
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
    if (url === "/api/v1/providers") return new Response(JSON.stringify({ endpoints: [{ id: "primary", host: "news.example", port: 563, tls: true, primary: true, priority: 1 }] }), { status: 200 });
    if (url === "/api/v1/providers/primary/qualifications") return new Response(JSON.stringify({ qualifications: [{ endpoint: "primary", created_at: "2026-09-30T00:00:00Z", result: { capabilities: ["READER"], overview_code: 224, overview_rows: 1, overview_dates: 1 } }] }), { status: 200 });
    if (url === "/api/v1/newsgroups") return new Response(JSON.stringify({ newsgroups: [{ name: "comp.lang.go", articles: 2 }] }), { status: 200 });
    if (url === "/api/v1/coverage") return new Response(JSON.stringify({ coverage: [{ endpoint: "primary", newsgroup: "comp.lang.go", state: "complete", article_number_start: 1, article_number_end: 2 }] }), { status: 200 });
    return new Response("{}", { status: 500 });
  });
  vi.stubGlobal("fetch", fetchMock);
  await act(async () => { root.render(<App />); });
  const dates = document.querySelectorAll('input[type="date"]') as NodeListOf<HTMLInputElement>;
  await act(async () => { setValue(dates[0], "2020-01-01"); setValue(dates[1], "2020-01-02"); });
  await click("Queue job"); await click("Pause"); await click("Resume"); await click("Cancel");
  await click("Search"); await click("Load more");
  await act(async () => { (document.querySelector('input[aria-label="Select <a@test>"]') as HTMLInputElement).click(); });
  await click("Mark selected unwanted"); await click("Search"); await click("A subject"); await click("Retrieve text"); await click("Show configured endpoints"); await click("Show history"); await click("Show stored groups and coverage");
  expect(document.body.textContent).toContain("body text");
  expect(document.body.textContent).toContain("news.example:563");
  expect(document.body.textContent).toContain("comp.lang.go (2)");
  expect(document.body.textContent).toContain("Provider qualification history");
});
