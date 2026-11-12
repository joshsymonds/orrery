import assert from "node:assert/strict";
import { test } from "node:test";

import { createClients, createTransport } from "./client";
import { start } from "./main";
import { installTestDom, removeTestDom } from "./testdom";

const panesPath = "/orrery.v1.Panes/List";
const sessionsPath = "/orrery.v1.Sessions/List";
const intervalMs = 5;

// Synthetic fixtures: one pane and one headless session, no real hosts or paths.
function paneBody(label: string) {
  return {
    panes: [
      {
        id: "%1",
        host: "box",
        session: "work",
        label,
        cwd: "/work/api",
        state: "waiting",
        summary: "building",
        question: "",
        changed: "40",
        seen: "10",
      },
    ],
  };
}

const sessionBody = {
  sessions: [
    {
      id: "s1",
      pane: "",
      label: "notes",
      state: "working",
      summary: "",
      question: "",
      changed: "30",
      seen: "30",
    },
  ],
};

interface FakeServer {
  fetch: typeof globalThis.fetch;
  // The pane label the next Panes.List answers with.
  paneLabel: string;
  // While set, Panes.List waits for it before answering.
  gate: Promise<void> | null;
  panesCalls: number;
  panesInFlight: number;
  maxPanesInFlight: number;
}

// fakeServer answers both Connect lists from its fields, so a test can change what the next reload shows.
function fakeServer(paneLabel: string): FakeServer {
  const server: FakeServer = {
    fetch: async (input, init) => {
      const request = new Request(input, init);
      const path = new URL(request.url).pathname;
      if (path === sessionsPath) {
        return json(sessionBody);
      }
      if (path !== panesPath) {
        return new Response("{}", { status: 404, headers: { "content-type": "application/json" } });
      }
      server.panesCalls++;
      server.panesInFlight++;
      server.maxPanesInFlight = Math.max(server.maxPanesInFlight, server.panesInFlight);
      try {
        if (server.gate) {
          await server.gate;
        } else {
          // Longer than intervalMs, so a reload that ignored the in-flight one would overlap it.
          await new Promise((resolve) => setTimeout(resolve, intervalMs * 3));
        }
        return json(paneBody(server.paneLabel));
      } finally {
        server.panesInFlight--;
      }
    },
    paneLabel,
    gate: null,
    panesCalls: 0,
    panesInFlight: 0,
    maxPanesInFlight: 0,
  };
  return server;
}

function json(body: unknown): Response {
  return new Response(JSON.stringify(body), { status: 200, headers: { "content-type": "application/json" } });
}

function clientsFor(server: FakeServer): ReturnType<typeof createClients> {
  return createClients(createTransport({ baseUrl: "https://orrery.test", fetch: server.fetch }));
}

async function until(check: () => boolean, what: string): Promise<void> {
  const deadline = Date.now() + 2000;
  while (!check()) {
    if (Date.now() > deadline) {
      throw new Error(`timed out waiting for ${what}`);
    }
    await new Promise((resolve) => setTimeout(resolve, 2));
  }
}

function labels(root: HTMLElement): string[] {
  return [...root.querySelectorAll(".label")].map((element) => element.textContent ?? "");
}

// withPage runs check with a fresh DOM, a root element for start, and the test's own cleanup.
async function withPage(check: (root: HTMLElement) => Promise<void>): Promise<void> {
  const window = installTestDom();
  const root = document.createElement("div");
  try {
    await check(root);
  } finally {
    await removeTestDom(window);
  }
}

test("loaded rows render into the root", () =>
  withPage(async (root) => {
    const server = fakeServer("api");
    const stop = start(root, clientsFor(server), intervalMs);
    try {
      await until(() => labels(root).includes("api"), "the pane row");
      assert.ok(labels(root).includes("notes"), `labels were ${JSON.stringify(labels(root))}`);
    } finally {
      stop();
    }
  }));

test("before the first load lands the root holds no rows", () =>
  withPage(async (root) => {
    const server = fakeServer("api");
    let release: () => void = () => {};
    server.gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    const stop = start(root, clientsFor(server), intervalMs);
    try {
      assert.deepEqual(labels(root), []);
      release();
      await until(() => labels(root).includes("api"), "the pane row after release");
    } finally {
      stop();
    }
  }));

test("a reload shows the changed pane", () =>
  withPage(async (root) => {
    const server = fakeServer("api");
    const stop = start(root, clientsFor(server), intervalMs);
    try {
      await until(() => labels(root).includes("api"), "the first pane label");
      server.paneLabel = "web";
      await until(() => labels(root).includes("web") && !labels(root).includes("api"), "the renamed pane");
    } finally {
      stop();
    }
  }));

test("a failed Panes.List shows the fixed error and keeps the sessions", () =>
  withPage(async (root) => {
    const failing: typeof globalThis.fetch = async (input, init) => {
      const path = new URL(new Request(input, init).url).pathname;
      if (path === panesPath) {
        return new Response(JSON.stringify({ code: "unavailable", message: "panes server detail" }), {
          status: 503,
          headers: { "content-type": "application/json" },
        });
      }
      return json(sessionBody);
    };
    const clients = createClients(createTransport({ baseUrl: "https://orrery.test", fetch: failing }));
    const stop = start(root, clients, intervalMs);
    try {
      await until(() => root.querySelector(".error") !== null, "the error line");
      assert.equal(root.querySelector(".error")?.textContent, "panes unavailable");
      assert.ok(labels(root).includes("notes"), `labels were ${JSON.stringify(labels(root))}`);
    } finally {
      stop();
    }
  }));

test("a reload never starts while the last one is in flight", () =>
  withPage(async (root) => {
    const server = fakeServer("api");
    const stop = start(root, clientsFor(server), intervalMs);
    try {
      await until(() => server.panesCalls >= 3, "three loads");
      assert.equal(server.maxPanesInFlight, 1);
    } finally {
      stop();
    }
  }));

test("stop unmounts the root and stops reloading", () =>
  withPage(async (root) => {
    const server = fakeServer("api");
    const stop = start(root, clientsFor(server), intervalMs);
    await until(() => labels(root).includes("api"), "the first load");
    stop();
    assert.equal(root.childNodes.length, 0, "stop left nodes in the root");
    const callsAtStop = server.panesCalls;
    await new Promise((resolve) => setTimeout(resolve, intervalMs * 10));
    assert.equal(server.panesCalls, callsAtStop, "a reload ran after stop");
    assert.equal(root.childNodes.length, 0, "a reload rendered after stop");
  }));
