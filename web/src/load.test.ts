import assert from "node:assert/strict";
import { test } from "node:test";

import { createClients, createTransport } from "./client";
import { loadRows } from "./load";

const panesPath = "/orrery.v1.Panes/List";
const sessionsPath = "/orrery.v1.Sessions/List";
const headlessPath = "/orrery.v1.Sessions/ListHeadless";

// Synthetic fixtures: one pane and one headless session, no real hosts or paths.
const syntheticPane = {
  id: "%1",
  host: "box",
  session: "work",
  label: "api",
  cwd: "/work/api",
  state: "waiting",
  summary: "building",
  question: "",
  claudeSession: "claude-1",
  changed: "40",
  seen: "10",
};

const syntheticSession = {
  id: "s1",
  pane: "",
  label: "notes",
  state: "working",
  summary: "",
  question: "",
  changed: "30",
  seen: "30",
};

// A headless session as Sessions.ListHeadless answers it, under a synthetic directory, with its served headline.
const syntheticHeadless = {
  id: "170bf619aa00bb11cc22dd33ee44ff55",
  dir: "/work/gatehouse",
  headline: "Billing rounding plan",
  state: "working",
  changed: "50",
  seen: "0",
  summary: "",
  question: "",
};

interface Answer {
  status: number;
  body: unknown;
}

// answersFor returns a fake fetch that answers each Connect path from the given table. A path it was not
// given is recorded in unexpected, so a test fails rather than reading an unanswered call as a failure.
function answersFor(answers: Record<string, Answer>, unexpected: string[]): typeof globalThis.fetch {
  return async (input, init) => {
    const request = new Request(input, init);
    const path = new URL(request.url).pathname;
    const answer = answers[path];
    if (!answer) {
      unexpected.push(path);
      return new Response("{}", { status: 404, headers: { "content-type": "application/json" } });
    }
    return new Response(JSON.stringify(answer.body), {
      status: answer.status,
      headers: { "content-type": "application/json" },
    });
  };
}

function clientsWith(answers: Record<string, Answer>): { clients: ReturnType<typeof createClients>; unexpected: string[] } {
  const unexpected: string[] = [];
  const transport = createTransport({ baseUrl: "https://orrery.test", fetch: answersFor(answers, unexpected) });
  return { clients: createClients(transport), unexpected };
}

// The store position Sessions.List reports, as proto JSON writes an int64.
const syntheticSeq = "42";

const answered: Record<string, Answer> = {
  [panesPath]: { status: 200, body: { panes: [syntheticPane] } },
  [sessionsPath]: { status: 200, body: { sessions: [syntheticSession], seq: syntheticSeq } },
  [headlessPath]: { status: 200, body: { sessions: [syntheticHeadless] } },
};

const sessionsUnavailable: Answer = {
  status: 503,
  body: { code: "unavailable", message: "sessions server detail" },
};

const panesUnavailable: Answer = {
  status: 503,
  body: { code: "unavailable", message: "panes server detail" },
};

test("every list answers: rows come from the panes, the sessions and the headless sessions, with no error", async () => {
  const { clients, unexpected } = clientsWith(answered);

  const result = await loadRows(clients);

  assert.deepEqual(unexpected, []);
  assert.equal(result.error, "");
  assert.equal(result.seq, 42n);
  assert.deepEqual(
    result.rows.map((row) => [row.kind, row.key, row.label, row.project]),
    [
      ["pane", "pane:box:%1", "building", "/work/api"],
      ["session", "session:s1", "notes", ""],
      ["session", `session:${syntheticHeadless.id}`, "Billing rounding plan", "/work/gatehouse"],
    ],
  );
});

test("Sessions.ListHeadless fails: rows come from the panes and the sessions, with no error", async () => {
  const { clients, unexpected } = clientsWith({
    [panesPath]: answered[panesPath],
    [sessionsPath]: answered[sessionsPath],
    [headlessPath]: sessionsUnavailable,
  });

  const result = await loadRows(clients);

  assert.deepEqual(unexpected, []);
  assert.equal(result.error, "");
  assert.equal(result.seq, 42n);
  assert.deepEqual(
    result.rows.map((row) => row.key),
    ["pane:box:%1", "session:s1"],
  );
});

test("Sessions.List fails: rows come from the panes and the headless sessions, with no error", async () => {
  const { clients, unexpected } = clientsWith({
    [panesPath]: answered[panesPath],
    [sessionsPath]: sessionsUnavailable,
    [headlessPath]: answered[headlessPath],
  });

  const result = await loadRows(clients);

  assert.deepEqual(unexpected, []);
  assert.equal(result.error, "");
  assert.equal(result.seq, 0n);
  assert.deepEqual(
    result.rows.map((row) => row.key),
    ["pane:box:%1", `session:${syntheticHeadless.id}`],
  );
});

test("Panes.List fails: the error names the panes list without server text, and rows come from the sessions and the headless sessions", async () => {
  const { clients, unexpected } = clientsWith({
    [panesPath]: panesUnavailable,
    [sessionsPath]: answered[sessionsPath],
    [headlessPath]: answered[headlessPath],
  });

  const result = await loadRows(clients);

  assert.deepEqual(unexpected, []);
  assert.equal(result.error, "panes unavailable");
  assert.ok(!result.error.includes("server detail"), `error carried server text: ${result.error}`);
  assert.equal(result.seq, 42n);
  assert.deepEqual(
    result.rows.map((row) => row.key),
    ["session:s1", `session:${syntheticHeadless.id}`],
  );
});

test("both calls are in flight at once: the panes answer waits for the sessions call to start", async () => {
  let releasePanes: () => void = () => {};
  const sessionsStarted = new Promise<void>((resolve) => {
    releasePanes = resolve;
  });
  const unexpected: string[] = [];
  const fakeFetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const request = new Request(input, init);
    const path = new URL(request.url).pathname;
    if (path === panesPath) {
      await sessionsStarted;
      return new Response(JSON.stringify(answered[panesPath].body), {
        status: 200,
        headers: { "content-type": "application/json" },
      });
    }
    if (path === sessionsPath) {
      releasePanes();
      return new Response(JSON.stringify(answered[sessionsPath].body), {
        status: 200,
        headers: { "content-type": "application/json" },
      });
    }
    if (path === headlessPath) {
      return new Response(JSON.stringify(answered[headlessPath].body), {
        status: 200,
        headers: { "content-type": "application/json" },
      });
    }
    unexpected.push(path);
    return new Response("{}", { status: 404, headers: { "content-type": "application/json" } });
  };
  const transport = createTransport({ baseUrl: "https://orrery.test", fetch: fakeFetch });

  const result = await loadRows(createClients(transport));

  assert.deepEqual(unexpected, []);
  assert.equal(result.error, "");
  assert.equal(result.rows.length, 3);
});
