import assert from "node:assert/strict";
import { test } from "node:test";

import { type JSX, render } from "preact";
import { act } from "preact/test-utils";

import type { ArchiveControl } from "./epicline";
import { InboxRow } from "./inboxrow";
import type { Row } from "./rows";
import { installTestDom, removeTestDom } from "./testdom";

// Each test's name carries the token AppSplit: the inbox row renders on its own, outside App.

const now = 1000;

const idleArchive: ArchiveControl = { archived: false, open: false, run: () => {}, toggle: () => {}, close: () => {} };

// A waiting pane row whose stored question offers two options, numbered 1 and 2 in their labels.
const waitingQuestion = JSON.stringify({
  questions: [{ question: "Which way?", options: [{ label: "1 Left" }, { label: "2 Right" }] }],
});

function waitingRow(): Row {
  return {
    tool: "",
    key: "pane:%3",
    id: "%3",
    archiveId: "",
    kind: "pane",
    label: "build",
    project: "/work/app",
    host: "box",
    state: "waiting",
    summary: "",
    question: waitingQuestion,
    questionHash: "hash",
    changed: now - 60,
    seen: 0,
    unseen: true,
    archived: false,
    archiveKind: "none",
  };
}

async function withRow(element: JSX.Element, check: (container: HTMLElement) => void | Promise<void>): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(element, container);
    await check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

test("AppSplit: a waiting row draws the QUESTION type and its option chips outside App", () =>
  withRow(
    <InboxRow
      row={waitingRow()}
      now={now}
      cursor={false}
      archive={idleArchive}
      conversation={null}
      onOpen={() => {}}
      onSeen={() => {}}
    />,
    (container) => {
      assert.equal(container.querySelector(".ty")?.textContent, "QUESTION");
      // Each chip reads its key cap, then its label with the number its key cap already shows removed.
      assert.deepEqual(
        [...container.querySelectorAll(".opt")].map((chip) => chip.textContent),
        ["1Left", "2Right"],
      );
    },
  ));

test("AppSplit: the cursor's row draws b-row cur, and a row the cursor is not on draws b-row alone", async () => {
  await withRow(
    <InboxRow
      row={waitingRow()}
      now={now}
      cursor={true}
      archive={idleArchive}
      conversation={null}
      onOpen={() => {}}
      onSeen={() => {}}
    />,
    (container) => {
      assert.equal(container.querySelector(".b-row")?.getAttribute("class"), "b-row cur");
      assert.equal(container.querySelector(".b-row")?.getAttribute("aria-current"), "true");
    },
  );
  await withRow(
    <InboxRow
      row={waitingRow()}
      now={now}
      cursor={false}
      archive={idleArchive}
      conversation={null}
      onOpen={() => {}}
      onSeen={() => {}}
    />,
    (container) => {
      assert.equal(container.querySelector(".b-row")?.getAttribute("class"), "b-row");
      assert.equal(container.querySelector(".b-row")?.hasAttribute("aria-current"), false);
    },
  );
});

test("AppSplit: a click calls onOpen, a double-click calls onSeen, and each calls only its own", () => {
  const opened: string[] = [];
  const seen: string[] = [];
  return withRow(
    <InboxRow
      row={waitingRow()}
      now={now}
      cursor={false}
      archive={idleArchive}
      conversation={null}
      onOpen={() => opened.push("open")}
      onSeen={() => seen.push("seen")}
    />,
    async (container) => {
      const row = container.querySelector<HTMLElement>(".b-row");
      assert.ok(row !== null, "the row is drawn");
      await act(() => {
        row.dispatchEvent(new Event("click"));
      });
      assert.deepEqual(opened, ["open"]);
      assert.deepEqual(seen, []);
      await act(() => {
        row.dispatchEvent(new Event("dblclick"));
      });
      assert.deepEqual(opened, ["open"]);
      assert.deepEqual(seen, ["seen"]);
    },
  );
});
