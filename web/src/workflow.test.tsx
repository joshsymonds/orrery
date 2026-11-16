import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";

import type { LoadArtifact } from "./evidence";
import { type EpicNode, EpicNodeSchema, type EpicsGetResponse, EpicsGetResponseSchema } from "./gen/orrery/v1/epics_pb";
import { installTestDom, removeTestDom } from "./testdom";
import { Workflow } from "./workflow";

function detail(init: MessageInitShape<typeof EpicsGetResponseSchema>): EpicsGetResponse {
  return create(EpicsGetResponseSchema, init);
}

// node is one workflow node run as the response carries it: the fields its kind holds are set only where a test needs them.
function node(init: MessageInitShape<typeof EpicNodeSchema>): EpicNode {
  return create(EpicNodeSchema, init);
}

const epic = { id: "e1", slug: "demo-epic", repository: "demo-repo", outcome: "ship the demo" };

// noArtifacts answers no artifact: a test that draws no image or log never calls it.
const noArtifacts: LoadArtifact = () => Promise.reject(new Error("the view loaded an artifact no test expected"));

async function withView(
  init: MessageInitShape<typeof EpicsGetResponseSchema>,
  check: (container: HTMLElement) => void | Promise<void>,
  loadArtifact: LoadArtifact = noArtifacts,
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  const { nodes } = detail(init);
  try {
    render(<Workflow nodes={nodes} loadArtifact={loadArtifact} />, container);
    await check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

// settled polls until holds is true, a turn of the event loop at a time, for up to a second. Preact runs the effects of
// a render after a paint timer, so an image's load lands on a later turn than the render.
async function settled(holds: () => boolean): Promise<void> {
  for (let turn = 0; turn < 100 && !holds(); turn += 1) {
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
}

// rowsOf are the node rows of the WORKFLOW section, in the order the view draws them.
function rowsOf(container: HTMLElement): Element[] {
  return [...container.querySelectorAll("section > ol > li")];
}

// spansStarting are the texts of a row's spans that begin with prefix, in order.
function spansStarting(row: Element, prefix: string): (string | null)[] {
  return [...row.querySelectorAll("span")]
    .map((span) => span.textContent)
    .filter((text) => text?.startsWith(prefix) === true);
}

// listsOf are the texts of each list's items in a row, one array per list in order.
function listsOf(row: Element): (string | null)[][] {
  return [...row.querySelectorAll("ul")].map((list) => [...list.children].map((item) => item.textContent));
}

test("the WORKFLOW section lists the nodes in the order given, each with its path, kind, state, outcome and round", () =>
  withView(
    {
      epic,
      nodes: [
        node({ path: "1", node: "plan", kind: "plan", state: "ended", outcome: "passed" }),
        node({ path: "3", node: "loop", kind: "loop", state: "running", loopRound: 2n }),
        node({ path: "3/1", node: "check", kind: "gate", state: "ended", outcome: "failed", round: 2n, attempt: 1n }),
      ],
    },
    (container) => {
      assert.equal(container.querySelector("section > h3.eyebrow")?.firstChild?.textContent ?? null, "WORKFLOW");
      assert.equal(container.querySelector("section > h3.eyebrow > span.sub")?.textContent ?? null, "3");
      const rows = rowsOf(container);
      assert.equal(rows.length, 3);
      assert.deepEqual(
        rows.map((row) => row.querySelector(".path")?.textContent ?? null),
        ["1", "3", "3/1"],
      );
      assert.deepEqual(
        rows.map((row) => row.querySelector(".kind")?.textContent ?? null),
        ["plan", "loop", "gate"],
      );
      assert.deepEqual(
        rows.map((row) => row.querySelector(".state")?.textContent ?? null),
        ["ended", "running", "ended"],
      );
      assert.deepEqual(
        rows.map((row) => row.querySelector(".end")?.textContent ?? null),
        ["passed", null, "failed"],
        "an outcome shows once the run has one",
      );
      assert.deepEqual(
        rows.map((row) => spansStarting(row, "round ")),
        [["round 0"], ["round 0"], ["round 2"]],
      );
    },
  ));

test("a loop round and an attempt draw only when set", () =>
  withView(
    {
      epic,
      nodes: [
        node({ path: "1", node: "plan", kind: "plan", state: "ended" }),
        node({ path: "3", node: "loop", kind: "loop", state: "running", loopRound: 2n }),
        node({ path: "3/1", node: "check", kind: "gate", state: "ended", round: 2n, attempt: 1n }),
      ],
    },
    (container) => {
      const rows = rowsOf(container);
      assert.deepEqual(
        rows.map((row) => spansStarting(row, "loop round ")),
        [[], ["loop round 2"], []],
      );
      assert.deepEqual(
        rows.map((row) => spansStarting(row, "attempt ")),
        [[], [], ["attempt 1"]],
      );
    },
  ));

test("a plan's turns draw as rows, then its tasks; a build's task draws as its one row", () =>
  withView(
    {
      epic,
      nodes: [
        node({ path: "1", node: "plan", kind: "plan", state: "ended", turns: [3n, 4n], tasks: [7n] }),
        node({ path: "2", node: "build", kind: "build", state: "running", tasks: [5n] }),
      ],
    },
    (container) => {
      const rows = rowsOf(container);
      assert.deepEqual(listsOf(rows[0]), [["turn 3", "turn 4"], ["task 7"]]);
      assert.deepEqual(listsOf(rows[1]), [["task 5"]]);
    },
  ));

// EvidenceRuns' own fields are its tests'; this one pins only that a node's runs reach it in order with loadArtifact.
test("an evidence node's runs reach EvidenceRuns in order, with the loader its kept image draws through", () => {
  const pngBytes = new Uint8Array([137, 80, 78, 71]);
  const load: LoadArtifact = (run, name) =>
    run === "r-green" && name === "shots/after.png"
      ? Promise.resolve({ data: pngBytes, mediaType: "image/png" })
      : Promise.reject(new Error(`no artifact ${run}/${name}`));
  return withView(
    {
      epic,
      nodes: [
        node({
          path: "4",
          node: "shots",
          kind: "evidence",
          state: "ended",
          outcome: "passed",
          runs: [
            { id: "r-red", phase: "red", source: "captured", state: "ended", verdict: "fails", passed: true, exit: 1 },
            {
              id: "r-green",
              phase: "green",
              source: "supplied",
              state: "ended",
              verdict: "holds",
              passed: true,
              exit: 0,
              artifacts: [{ path: "shots/after.png", state: "kept", size: 4n, mediaType: "image/png" }],
            },
          ],
        }),
      ],
    },
    async (container) => {
      await settled(() => container.querySelector("img") !== null);
      const row = rowsOf(container)[0];
      assert.equal(row.querySelectorAll("ul.runs > li.run").length, 2);
      assert.deepEqual(
        [...row.querySelectorAll(".run .phase")].map((phase) => phase.textContent),
        ["red", "green"],
      );
      assert.equal(row.querySelector("img")?.getAttribute("src")?.startsWith("blob:"), true);
    },
    load,
  );
});

test("a command's check draws its state, its exit once it ends, whether it timed out, and its output tails", () =>
  withView(
    {
      epic,
      nodes: [
        node({
          path: "5",
          node: "lint",
          kind: "command",
          state: "running",
          check: { state: "ended", exit: 2, timedOut: true, stdout: "FAIL\n", stderr: "boom\n" },
        }),
      ],
    },
    (container) => {
      const row = rowsOf(container)[0];
      assert.deepEqual(
        [...row.querySelectorAll(".state")].map((state) => state.textContent),
        ["running", "ended"],
        "the node's state, then the check's",
      );
      assert.deepEqual(spansStarting(row, "exit"), ["exit 2"]);
      assert.deepEqual(spansStarting(row, "timed out"), ["timed out"]);
      assert.deepEqual(
        [...row.querySelectorAll(".tails > *")].map((part) => part.textContent),
        ["stdout", "FAIL\n", "stderr", "boom\n"],
      );
    },
  ));

test("a check still running draws no exit, since its exit is not yet known", () =>
  withView(
    {
      epic,
      nodes: [node({ path: "5", node: "lint", kind: "command", state: "running", check: { state: "running" } })],
    },
    (container) => {
      const row = rowsOf(container)[0];
      assert.deepEqual(spansStarting(row, "exit"), []);
      assert.equal(row.querySelectorAll(".tails").length, 0);
    },
  ));

test("a check that did not run draws its reason and no exit", () =>
  withView(
    {
      epic,
      nodes: [
        node({
          path: "5",
          node: "gate",
          kind: "gate",
          state: "ended",
          outcome: "failed",
          check: { state: "ended", notRun: "no such command" },
        }),
      ],
    },
    (container) => {
      const row = rowsOf(container)[0];
      assert.deepEqual(spansStarting(row, "exit"), []);
      assert.deepEqual(spansStarting(row, "not run"), ["not run · no such command"]);
    },
  ));

test("an agent node draws its step id", () =>
  withView(
    { epic, nodes: [node({ path: "6", node: "review", kind: "agent", state: "running", step: "st-42" })] },
    (container) => {
      const row = rowsOf(container)[0];
      assert.deepEqual(
        [...row.querySelectorAll("p")].map((paragraph) => paragraph.textContent),
        ["step st-42"],
      );
    },
  ));

test("a node's output draws preformatted, and a node with no rows draws none", () =>
  withView(
    {
      epic,
      nodes: [
        node({ path: "7", node: "done", kind: "owned", state: "ended", outcome: "passed", output: "compiled\nlinked\n" }),
        node({ path: "8", node: "scope", kind: "owned", state: "ended", outcome: "passed" }),
      ],
    },
    (container) => {
      const [output, bare] = rowsOf(container);
      assert.deepEqual(
        [...output.querySelectorAll("pre")].map((pre) => pre.textContent),
        ["compiled\nlinked\n"],
      );
      assert.equal(bare.querySelectorAll("ul, p, pre, .runs, .tails").length, 0);
      assert.equal(bare.querySelectorAll(".state").length, 1);
    },
  ));

test("markup in a node's path, outcome, output, step and check tails draws as text with no element", () =>
  withView(
    {
      epic,
      nodes: [
        node({
          path: "<b>p</b>",
          node: "x",
          kind: "command",
          state: "ended",
          outcome: "<i>o</i>",
          output: "<script>alert(1)</script>",
          step: "<img src=x>",
          check: { state: "ended", stdout: "<img src=x onerror=alert(1)>" },
        }),
      ],
    },
    (container) => {
      assert.equal(container.querySelectorAll("b, i, script, img").length, 0);
      const row = rowsOf(container)[0];
      assert.equal(row.textContent?.includes("<b>p</b>"), true);
      assert.equal(row.textContent?.includes("<i>o</i>"), true);
      assert.equal(row.textContent?.includes("<script>alert(1)</script>"), true);
      assert.equal(row.textContent?.includes("<img src=x onerror=alert(1)>"), true);
    },
  ));
