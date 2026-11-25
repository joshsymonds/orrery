import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";

import { EvidenceRuns, type LoadArtifact } from "./evidence";
import {
  type EpicsGetResponse,
  EpicsGetResponseSchema,
  type EvidenceRun,
  EvidenceRunSchema,
} from "./gen/orrery/v1/epics_pb";
import { recordTasksOf } from "./record";
import { stagesOf } from "./taskstages";
import { installTestDom, removeTestDom } from "./testdom";

type RunInit = MessageInitShape<typeof EvidenceRunSchema>;

// The instant the stage rows are read at, in unix seconds. A fixed instant, so no clock is read.
const now = 2000;

function run(init: RunInit): EvidenceRun {
  return create(EvidenceRunSchema, init);
}

// noLog answers no load: a run's log is shut in every list drawn here, so no load is started.
const noLog: LoadArtifact = () => Promise.reject(new Error("no log is loaded while the log is shut"));

// drawnLineOf renders one run in the evidence list in a fresh DOM and calls check on its line.
async function drawnLineOf(evidenceRun: EvidenceRun, check: (line: Element) => void): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(<EvidenceRuns runs={[evidenceRun]} loadArtifact={noLog} />, container);
    const line = container.querySelector(".run-line");
    assert.ok(line, "the list draws the run's line");
    check(line);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

// stageSummaryOf is the summary of the stage row of task T1 at the given stage. The build record holds the run as that
// stage's run, and the task stands at that stage's ring.
function stageSummaryOf(stage: "red" | "green", stageRun: RunInit): string {
  const detail: EpicsGetResponse = create(EpicsGetResponseSchema, {
    buildTasks: [
      {
        id: "T1",
        title: "Parser",
        status: "open",
        stage,
        place: stage,
        tone: "run",
        requirements: ["R1"],
        red: stage === "red" ? stageRun : undefined,
        green: stage === "green" ? stageRun : undefined,
      },
    ],
  });
  const [task] = recordTasksOf(detail);
  assert.ok(task, "the build task is a record task");
  const row = stagesOf(detail, task, now).rows.find((candidate) => candidate.stage === stage);
  assert.ok(row, `the task has a ${stage} row`);
  return row.summary;
}

test("BriefOpen: a red run waiting for the gatherer reads that in its sentence, and in its live chip", () => {
  const waiting = run({ id: "r-waiting", phase: "red", source: "captured", state: "waiting" });
  return drawnLineOf(waiting, (line) => {
    assert.equal(line.querySelector(".sentence")?.textContent, "Red run: waiting for the gatherer…");
    assert.equal(line.querySelector(".chip")?.getAttribute("class"), "chip live");
    assert.equal(line.querySelector(".chip")?.textContent, "waiting for the gatherer");
  });
});

test("BriefOpen: a green run being gathered reads that in its sentence, and in its live chip", () => {
  const gathering = run({ id: "r-gathering", phase: "green", source: "captured", state: "gathering" });
  return drawnLineOf(gathering, (line) => {
    assert.equal(line.querySelector(".sentence")?.textContent, "Green run: being gathered…");
    assert.equal(line.querySelector(".chip")?.getAttribute("class"), "chip live");
    assert.equal(line.querySelector(".chip")?.textContent, "being gathered");
  });
});

test("BriefOpen: a run judging reads that a judge is reading the output, with a live judging chip", () => {
  const judging = run({ id: "r-judging", phase: "red", source: "captured", state: "judging" });
  return drawnLineOf(judging, (line) => {
    assert.equal(line.querySelector(".sentence")?.textContent, "Red run: a judge is reading the output…");
    assert.equal(line.querySelector(".chip")?.getAttribute("class"), "chip live");
    assert.equal(line.querySelector(".chip")?.textContent, "judging");
  });
});

test("BriefOpen: an ended red run that failed as it should reads its verdict in its sentence and its chip", () => {
  const ended = run({
    id: "r-ended",
    phase: "red",
    source: "captured",
    state: "ended",
    verdict: "fails",
    passed: true,
    exit: 1,
  });
  return drawnLineOf(ended, (line) => {
    assert.equal(
      line.querySelector(".sentence")?.textContent,
      "Red run: the test fails before any change, as it should.",
    );
    assert.equal(line.querySelector(".chip")?.getAttribute("class"), "chip bad");
    assert.equal(line.querySelector(".chip")?.textContent, "✗fails");
  });
});

test("BriefOpen: the red row of a task whose red run is waiting for the gatherer reads that", () => {
  const summary = stageSummaryOf("red", { id: "r-waiting", phase: "red", state: "waiting" });
  assert.equal(summary, "waiting for the gatherer");
});

test("BriefOpen: the green row of a task whose green run is being gathered reads being gathered", () => {
  const summary = stageSummaryOf("green", { id: "g-gathering", phase: "green", state: "gathering" });
  assert.equal(summary, "being gathered");
});

test("BriefOpen: the red row of a task whose red run is judging reads that a judge is reading the red run", () => {
  const summary = stageSummaryOf("red", { id: "r-judging", phase: "red", state: "judging" });
  assert.equal(summary, "a judge is reading the red run");
});

test("BriefOpen: the green row of a task whose green run ended and held reads its verdict, judged to hold", () => {
  const summary = stageSummaryOf("green", {
    id: "g-ended",
    phase: "green",
    state: "ended",
    passed: true,
    verdict: "holds",
    created: 300n,
    ended: 330n,
  });
  assert.equal(summary, "the test passes on the change · judged to hold");
});
