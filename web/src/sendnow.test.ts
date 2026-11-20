import assert from "node:assert/strict";
import { test } from "node:test";

import { sendNow } from "./sendnow";

// Send now over fakes that record what they are called with, in call order. Prompt ids come from a counter, so each
// say's id is known; nothing here waits on a timer.

interface Said {
  text: string;
  promptId: string;
}

function ids(): () => string {
  let next = 0;
  return () => {
    next += 1;
    return `id-${next}`;
  };
}

test("DockQueue: Send now calls the interrupt before any say", async () => {
  const calls: string[] = [];
  await sendNow(
    async () => {
      calls.push("interrupt");
      return ["first", "second"];
    },
    async (text) => {
      calls.push(`say ${text}`);
    },
    ids(),
  );
  assert.deepEqual(calls, ["interrupt", "say first", "say second"]);
});

test("DockQueue: Send now says every text again in send order, each with its own prompt id, and holds none", async () => {
  const said: Said[] = [];
  const result = await sendNow(
    async () => ["first", "second", "third"],
    async (text, promptId) => {
      said.push({ text, promptId });
    },
    ids(),
  );
  assert.deepEqual(said, [
    { text: "first", promptId: "id-1" },
    { text: "second", promptId: "id-2" },
    { text: "third", promptId: "id-3" },
  ]);
  assert.deepEqual(result, { held: [] });
});

test("DockQueue: when the second re-say is refused, Send now holds it and the third, in order, and says no more", async () => {
  const said: string[] = [];
  const result = await sendNow(
    async () => ["first", "second", "third"],
    async (text) => {
      if (text === "second") {
        throw new Error("refused");
      }
      said.push(text);
    },
    ids(),
  );
  assert.deepEqual(said, ["first"]);
  assert.deepEqual(result, { held: ["second", "third"] });
});

test("DockQueue: a rejected interrupt rejects Send now and says nothing", async () => {
  const said: string[] = [];
  await assert.rejects(
    sendNow(
      async () => {
        throw new Error("interrupt failed");
      },
      async (text) => {
        said.push(text);
      },
      ids(),
    ),
    /interrupt failed/,
  );
  assert.deepEqual(said, []);
});

test("DockQueue: an interrupt with no texts says nothing and holds nothing", async () => {
  const said: string[] = [];
  const result = await sendNow(
    async () => [],
    async (text) => {
      said.push(text);
    },
    ids(),
  );
  assert.deepEqual(said, []);
  assert.deepEqual(result, { held: [] });
});
