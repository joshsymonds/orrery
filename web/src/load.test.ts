import assert from "node:assert/strict";
import { test } from "node:test";

import { createClients, createTransport } from "./client";
import { loadRows } from "./load";

const panesPath = "/orrery.v1.Panes/List";
const sessionsPath = "/orrery.v1.Sessions/List";

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

const answered: Record<string, Answer> = {
  [panesPath]: { status: 200, body: { panes: [syntheticPane] } },
  [sessionsPath]: { status: 200, body: { sessions: [syntheticSession] } },
};

const sessionsUnavailable: Answer = {
  status: 503,
  body: { code: "unavailable", message: "sessions server detail" },
};

const panesUnavailable: Answer = {
  status: 503,
  body: { code: "unavailable", message: "panes server detail" },
};

test("both lists answer: rows come from the panes and the sessions, with no error", async () => {
  const { clients, unexpected } = clientsWith(answered);

  const result = await loadRows(clients);

  assert.deepEqual(unexpected, []);
  assert.equal(result.error, "");
  assert.deepEqual(
    result.rows.map((row) => [row.kind, row.key, row.label]),
    [
      ["pane", "pane:box:%1", "api"],
      ["session", "session:s1", "notes"],
    ],
  );
});

test("Sessions.List fails: rows come from the panes alone, with no error", async () => {
  const { clients, unexpected } = clientsWith({
    [panesPath]: answered[panesPath],
    [sessionsPath]: sessionsUnavailable,
  });

  const result = await loadRows(clients);

  assert.deepEqual(unexpected, []);
  assert.equal(result.error, "");
  assert.deepEqual(
    result.rows.map((row) => row.key),
    ["pane:box:%1"],
  );
});

test("Panes.List fails: the error names the panes list without server text, and rows come from the sessions alone", async () => {
  const { clients, unexpected } = clientsWith({
    [panesPath]: panesUnavailable,
    [sessionsPath]: answered[sessionsPath],
  });

  const result = await loadRows(clients);

  assert.deepEqual(unexpected, []);
  assert.equal(result.error, "panes unavailable");
  assert.ok(!result.error.includes("server detail"), `error carried server text: ${result.error}`);
  assert.deepEqual(
    result.rows.map((row) => row.key),
    ["session:s1"],
  );
});

test("both calls are in flight at once: the panes answer waits for the sessions call to start", { timeout: 2000 }, async () => {
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
    unexpected.push(path);
    return new Response("{}", { status: 404, headers: { "content-type": "application/json" } });
  };
  const transport = createTransport({ baseUrl: "https://orrery.test", fetch: fakeFetch });

  const result = await loadRows(createClients(transport));

  assert.deepEqual(unexpected, []);
  assert.equal(result.error, "");
  assert.equal(result.rows.length, 2);
});
