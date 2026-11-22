import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { EpicRow } from "./epicrow";
import { blockOf } from "./epics";
import { type EpicStep, EpicStepSchema, EpicSchema, EpicsGetResponseSchema } from "./gen/orrery/v1/epics_pb";
import { NowSection } from "./nowsection";
import { assertNone, installTestDom, removeTestDom } from "./testdom";
import type { EpicNode } from "./tree";

type StepInit = MessageInitShape<typeof EpicStepSchema>;

// The epic the rows belong to, and the clock both rows read their times against. Every test passes now, so none reads
// the wall clock.
const alpha = {
  id: "repo-a/alpha",
  repository: "repo-a",
  slug: "alpha",
  source: "orreryd",
  outcome: "building",
  live: true,
};
const now = 10_000;

// step is a running step of task api-1, built by builder and started at 8900, unless the init says otherwise.
function step(init: StepInit): EpicStep {
  return create(EpicStepSchema, {
    id: "s1",
    task: "api-1",
    agent: "builder",
    end: "",
    stageStarted: 9_000n,
    started: 8_900n,
    ...init,
  });
}

// Rows is the step rows the two places draw for the same running steps: the inbox row's, and the Now section's.
interface Rows {
  inbox: Element[];
  now: Element[];
}

// withRows draws the steps as an epic's inbox row draws its running steps, and as the Now section draws its rows, runs
// check on both, and removes both.
async function withRows(steps: EpicStep[], check: (rows: Rows) => void): Promise<void> {
  const window = installTestDom();
  const epic = create(EpicSchema, { ...alpha, runningSteps: steps });
  const hung: EpicNode = {
    kind: "epic",
    key: `epic:${alpha.id}`,
    block: blockOf(epic, undefined),
    listed: epic,
    contract: null,
  };
  const detail = create(EpicsGetResponseSchema, { epic: create(EpicSchema, alpha), steps });
  const inboxBox = document.createElement("div");
  const nowBox = document.createElement("div");
  document.body.append(inboxBox, nowBox);
  try {
    await act(() => {
      render(<EpicRow hung={hung} now={now} current={false} onSelect={() => {}} />, inboxBox);
      render(
        <NowSection
          detail={detail}
          now={now}
          narrow={false}
          numberKeys={false}
          onAnswer={() => Promise.resolve("")}
          onRetry={() => {}}
          onStop={() => {}}
          onResume={() => {}}
        />,
        nowBox,
      );
    });
    check({
      inbox: [...inboxBox.querySelectorAll(".row.step")],
      now: [...nowBox.querySelectorAll("div.tbl.steps > div.tr")],
    });
  } finally {
    await act(() => {
      render(null, inboxBox);
      render(null, nowBox);
    });
    inboxBox.remove();
    nowBox.remove();
    await removeTestDom(window);
  }
}

// texts is the text of each element of root that matches selector, in document order.
function texts(root: Element, selector: string): (string | null)[] {
  return Array.from(root.querySelectorAll(selector), (element) => element.textContent);
}

test("StepRows: a running step reads its purpose, then its doing line, in the inbox row and the Now row", () => {
  const purpose = "Refuse an export over an existing file.";
  const doing = "Writing the refusal test.";
  return withRows([step({ purpose, doing })], ({ inbox, now: nowRows }) => {
    assert.deepEqual(texts(inbox[0], "span.label, span.doing"), [purpose, doing]);
    assert.deepEqual(texts(nowRows[0], "span.strong, span.doing"), [purpose, doing]);
  });
});

test("StepRows: a step with no purpose reads its task, else its role, in the inbox row and the Now row", () =>
  withRows(
    [
      step({ id: "s1", task: "api-1", purpose: "", role: "implementer" }),
      step({ id: "s2", task: "", purpose: "", role: "reviewer", agent: "builder" }),
    ],
    ({ inbox, now: nowRows }) => {
      assert.deepEqual(
        inbox.map((row) => texts(row, "span.label")),
        [["api-1"], ["reviewer"]],
      );
      assert.deepEqual(
        nowRows.map((row) => texts(row, "span.strong")),
        [["api-1"], ["reviewer"]],
      );
    },
  ));

test("StepRows: the agent profile is in neither the inbox row nor the Now row", () =>
  withRows(
    [
      step({ id: "s1", task: "", purpose: "", role: "reviewer", agent: "builder", doing: "Reading the diff." }),
      step({ id: "s2", task: "api-1", purpose: "", role: "", agent: "builder" }),
    ],
    ({ inbox, now: nowRows }) => {
      for (const row of [...inbox, ...nowRows]) {
        assert.equal(row.textContent?.includes("builder"), false, "a row draws no agent profile");
      }
    },
  ));

test("StepRows: pips draw only for a step with a task, in the inbox row and the Now row", () =>
  withRows(
    [step({ id: "s1", task: "api-1", purpose: "" }), step({ id: "s2", task: "", purpose: "", role: "reviewer" })],
    ({ inbox, now: nowRows }) => {
      assert.equal(inbox[0].querySelectorAll("span.pips > i").length, 5, "a step with a task draws five pips");
      assertNone(inbox[1].querySelector("span.pips"), "a step with no task draws no pips");
      assert.equal(nowRows[0].querySelectorAll("span.pips > i").length, 5, "a step with a task draws five pips");
      assertNone(nowRows[1].querySelector("span.pips"), "a step with no task draws no pips");
    },
  ));
