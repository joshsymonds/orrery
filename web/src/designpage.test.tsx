import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { DesignPage, type DesignPageProps } from "./designpage";
import { at } from "./fixtures/clock";
import {
  DesignApproachSchema,
  DesignChoiceSchema,
  DesignContractSchema,
  DesignDecisionSchema,
  DesignDelegationSchema,
  DesignLogLineSchema,
  DesignQuestionSchema,
  DesignSchema,
  DesignStepSchema,
  DesignsGetResponseSchema,
  RecordMakerSchema,
  type Design,
  type DesignsGetResponse,
} from "./gen/orrery/v1/designs_pb";
import { noticeClock } from "./noticewords";
import type { QuestionAnswer } from "./questionthread";
import { assertNone, installTestDom, removeTestDom } from "./testdom";
import { turn } from "./testwait";

type ContractInit = MessageInitShape<typeof DesignContractSchema>;
type StageInit = MessageInitShape<typeof DesignsGetResponseSchema>["stage"];

// noAnswer is the answer a press that is not under test sends: it resolves and records nothing.
const noAnswer = async () => {};

// design is the invented design, with the given contracts.
function design(contracts: ContractInit[] = []): Design {
  return create(DesignSchema, {
    id: "invented-design",
    title: "Invented design",
    conversation: "invented-conversation",
    created: at("@13:10"),
    state: "Being researched",
    contracts,
  });
}

// contract is an invented contract of the design in the given state, at the given position in its start order.
function contract(id: string, state: string, position: bigint): ContractInit {
  return { id, title: id.split("/")[1] ?? id, repository: "invented", state, position };
}

// served is the response the server serves for a design: its record, and the parts given.
function served(record: Design, init: MessageInitShape<typeof DesignsGetResponseSchema> = {}): DesignsGetResponse {
  return create(DesignsGetResponseSchema, { ...init, design: record });
}

// research is a research step of the design, returned with the given subject and report.
function research(step: string, subject: string, report: string) {
  return create(DesignStepSchema, { step, kind: "research", subject, status: "returned", report, created: at("@13:20") });
}

// makerOf is who made a record, with the fields the kind uses.
function makerOf(init: MessageInitShape<typeof RecordMakerSchema>) {
  return create(RecordMakerSchema, init);
}

// logLine is a line of the design's log, at the given time, by the given maker kind, with what it did and its words.
function logLine(seq: bigint, time: bigint, kind: string, what: string, ids: string[], words = "") {
  return create(DesignLogLineSchema, { seq, time, maker: makerOf({ kind }), what, ids, words });
}

// withPage renders DesignPage with the props given into a fresh container, runs check on it, and removes it. The props not
// given are a design with no contracts, no record, no error, and presses that do nothing.
async function withPage(props: Partial<DesignPageProps>, check: (container: HTMLElement) => Promise<void> | void): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  try {
    await act(() => {
      render(
        <DesignPage
          design={design()}
          get={null}
          error=""
          onOpenContract={() => undefined}
          onAnswer={noAnswer}
          {...props}
        />,
        container,
      );
    });
    await check(container);
  } finally {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  }
}

// stageNodes returns the stages of the design's strip, in order.
function stageNodes(root: ParentNode): HTMLElement[] {
  return [...root.querySelectorAll<HTMLElement>('nav[aria-label="where this design is"] > [data-stage]')];
}

// currentStage returns the name of the stage the strip marks as the current step, or null when it marks none.
function currentStage(root: ParentNode): string | null {
  const marked = root.querySelector<HTMLElement>('nav[aria-label="where this design is"] [aria-current="step"]');
  return marked?.getAttribute("data-stage") ?? null;
}

// sectionNamed returns the section labelled name, and fails when none is drawn for it.
function sectionNamed(root: ParentNode, name: string): HTMLElement {
  const found = root.querySelector<HTMLElement>(`section[aria-label="${name}"]`);
  assert.ok(found, `the section "${name}" is drawn`);
  return found;
}

// fieldOf returns the description that follows the term label in root, or null when root has no such term.
function fieldOf(root: ParentNode, label: string): string | null {
  const term = [...root.querySelectorAll("dt")].find((candidate) => candidate.textContent === label);
  return term?.nextElementSibling?.textContent ?? null;
}

// press clicks the button named name inside root, then lets the call it makes settle and its state render.
async function press(root: ParentNode, name: string): Promise<void> {
  const button = [...root.querySelectorAll<HTMLButtonElement>("button")].find(
    (candidate) => (candidate.textContent ?? "").trim() === name,
  );
  assert.ok(button, `a button "${name}" is drawn`);
  await act(async () => {
    button.click();
    await turn();
  });
}

test("DesignPage: the stage strip's current stage is the first of the rules that holds, for several records", async () => {
  const question = create(DesignQuestionSchema, { id: 1n, text: "Which?" });
  const approach = create(DesignApproachSchema, { seq: 1n, name: "Pin the database" });
  const closure = create(DesignStepSchema, { step: "c1", kind: "closure" });
  const cases: { words: string; contracts: ContractInit[]; stage: StageInit; approaches: boolean; current: string | null }[] = [
    { words: "research alone", contracts: [], stage: { steps: [research("r1", "the drafts", "")] }, approaches: false, current: "Research" },
    { words: "a question", contracts: [], stage: { questions: [question] }, approaches: false, current: "Questions" },
    { words: "an approach, over a question", contracts: [], stage: { questions: [question] }, approaches: true, current: "Design" },
    { words: "a contract being drafted", contracts: [contract("invented/drafting", "drafting", 1n)], stage: {}, approaches: false, current: "Design" },
    { words: "a discovery step", contracts: [], stage: { steps: [create(DesignStepSchema, { step: "d1", kind: "discovery" })] }, approaches: false, current: "Steelman" },
    { words: "a closure step", contracts: [], stage: { steps: [closure] }, approaches: false, current: "Closure" },
    { words: "closure skipped", contracts: [], stage: { closureSkipped: true }, approaches: false, current: "Closure" },
    { words: "a proposed contract, over a closure step", contracts: [contract("invented/proposed", "proposed", 1n)], stage: { steps: [closure] }, approaches: false, current: "Proposed" },
    { words: "every contract accepted, so no stage is current", contracts: [contract("invented/accepted", "accepted", 1n)], stage: { closureSkipped: true }, approaches: false, current: null },
  ];
  for (const { words, contracts, stage, approaches, current } of cases) {
    const record = design(contracts);
    const get = served(record, { stage, approaches: approaches ? [approach] : [] });
    await withPage({ design: record, get }, (container) => {
      assert.equal(currentStage(container), current, `${words}: the current stage is not ${current}`);
    });
  }
});

test("DesignPage: the stage strip is labelled where this design is, has the stages before the current one done, and says not reached after it", async () => {
  const record = design();
  const get = served(record, {
    stage: { steps: [research("r1", "the drafts", "")] },
    approaches: [create(DesignApproachSchema, { seq: 1n, name: "Pin the database" })],
  });
  await withPage({ design: record, get }, (container) => {
    assert.ok(container.querySelector('nav[aria-label="where this design is"]'), "the strip is not a nav labelled where this design is");
    const nodes = stageNodes(container);
    assert.deepEqual(
      nodes.map((node) => node.getAttribute("data-stage")),
      ["Research", "Questions", "Design", "Steelman", "Closure", "Proposed"],
      "the six stages are not drawn in order",
    );
    assert.deepEqual(
      nodes.map((node) => (node.classList.contains("done") ? "done" : node.classList.contains("now") ? "now" : "ahead")),
      ["done", "done", "now", "ahead", "ahead", "ahead"],
      "the stages are not done before, current at, and ahead of the reached one",
    );
    assert.deepEqual(
      nodes.map((node) => node.querySelector(".ds")?.textContent),
      ["", "", "", "not reached", "not reached", "not reached"],
      "a later stage does not say not reached",
    );
  });
});

test("DesignPage: each research step draws its subject, its status, and its scout report's answer, evidence and what was not inspected", async () => {
  const report = JSON.stringify({
    status: "done",
    answer: "The export reads the database pinned in the vendored tree.",
    evidence: ["export/tz.go:42 reads the pinned copy", "export/tz_test.go:17 pins the version"],
    not_inspected: "the upstream release notes",
  });
  const record = design();
  const get = served(record, {
    stage: { steps: [research("step-1", "how the export reads the tz database", report)] },
  });
  await withPage({ design: record, get }, (container) => {
    const step = container.querySelector<HTMLElement>('[data-step="step-1"]');
    assert.ok(step, "the research step is not drawn");
    assert.equal(step.querySelector(".design-research-subject")?.textContent, "how the export reads the tz database");
    assert.equal(step.querySelector(".chip")?.textContent, "returned");
    assert.equal(fieldOf(step, "Answer"), "The export reads the database pinned in the vendored tree.");
    assert.deepEqual(
      [...step.querySelectorAll("dd code")].map((line) => line.textContent),
      ["export/tz.go:42 reads the pinned copy", "export/tz_test.go:17 pins the version"],
      "each evidence line is not drawn in code",
    );
    assert.equal(fieldOf(step, "Not inspected"), "the upstream release notes");
  });
});

test("DesignPage: a research report that is not the scout's return is drawn as its text in pre", async () => {
  const text = "The backfill is idempotent on the key, as far as the first read shows.";
  const record = design();
  const get = served(record, { stage: { steps: [research("step-2", "whether the backfill can run twice", text)] } });
  await withPage({ design: record, get }, (container) => {
    const step = container.querySelector<HTMLElement>('[data-step="step-2"]');
    assert.ok(step, "the research step is not drawn");
    assert.equal(step.querySelector("pre")?.textContent, text, "the report's text is not drawn in pre");
    assertNone(step.querySelector(".design-report"), "a report that is not the scout's return is drawn as the scout's parts");
  });
});

test("DesignPage: each decision made for the owner is marked the Director under its delegation's message, with its kind, text and reason", async () => {
  const record = design();
  const get = served(record, {
    delegations: [
      create(DesignDelegationSchema, {
        seq: 1n,
        message: "Decide the small ones, and tell me the rest.",
        time: at("@13:45"),
        decisions: [
          create(DesignDecisionSchema, {
            kind: "finding",
            text: "Adopted the header change.",
            reason: "it only needs the text changed",
          }),
          create(DesignDecisionSchema, { kind: "question", text: "Kept the pin.", reason: "the recommendation" }),
        ],
      }),
    ],
  });
  await withPage({ design: record, get }, (container) => {
    const delegation = sectionNamed(container, "Decided for you").querySelector<HTMLElement>(".design-delegation");
    assert.ok(delegation, "the delegation is not drawn");
    assert.equal(
      delegation.querySelector(".design-delegation-message")?.textContent,
      "“Decide the small ones, and tell me the rest.”",
      "the delegation's message is not quoted",
    );
    const decisions = [...delegation.querySelectorAll<HTMLElement>("li.design-decision")];
    assert.equal(decisions.length, 2, "each decision is not drawn under its message");
    for (const decision of decisions) {
      assert.equal(decision.querySelector(".chip")?.textContent, "the Director", "a decision made for the owner is not marked the Director");
    }
    assert.equal(decisions[0]?.querySelector(".design-decision-kind")?.textContent, "finding");
    assert.equal(decisions[0]?.querySelector(".design-decision-text")?.textContent, "Adopted the header change.");
    assert.equal(decisions[0]?.querySelector(".design-decision-reason")?.textContent, "it only needs the text changed");
  });
});

test("DesignPage: each question draws as its own thread with the log lines that name it, and Accept answers it by its id", async () => {
  const answers: { question: bigint; answer: QuestionAnswer }[] = [];
  const record = design();
  const get = served(record, {
    stage: {
      questions: [
        create(DesignQuestionSchema, {
          id: 1n,
          text: "Bump the embedded tz database?",
          options: ["Keep the pin", "Allow the bump"],
          recommendation: "Keep the pin",
          asked: at("@13:50"),
        }),
        create(DesignQuestionSchema, {
          id: 2n,
          text: "Keep the old column?",
          options: ["Keep it"],
          recommendation: "Keep it",
          asked: at("@13:55"),
          answer: "Keep it",
          answered: at("@14:02"),
          accepted: true,
          maker: makerOf({ kind: "owner_serve" }),
        }),
      ],
    },
    log: [
      logLine(1n, at("@13:50"), "conversation", "asked", ["Q1"]),
      logLine(2n, at("@13:52"), "owner", "said", ["Q1"], "Which keeps the Lisbon case?"),
      logLine(3n, at("@13:55"), "conversation", "asked", ["Q2"]),
      logLine(4n, at("@13:56"), "orreryd", "recorded", []),
    ],
  });
  const onAnswer = async (question: bigint, answer: QuestionAnswer) => {
    answers.push({ question, answer });
  };
  await withPage({ design: record, get, onAnswer }, async (container) => {
    assert.equal(sectionNamed(container, "Questions").querySelector(".n")?.textContent, "1 of 2 settled", "the heading does not count the settled questions");
    const open = container.querySelector<HTMLElement>('[aria-label="question Q1"]');
    assert.ok(open, "Q1 is not drawn as its thread");
    assert.equal(open.querySelectorAll(".qt-msg").length, 2, "Q1's thread does not hold the two log lines that name it");
    assert.ok(container.querySelector('[aria-label="question Q2"]')?.classList.contains("settled"), "Q2 is not drawn settled");
    await press(open, "Accept");
    assert.deepEqual(answers, [{ question: 1n, answer: { accept: true } }], "Accept does not answer Q1 by its id");
  });
});

test("DesignPage: each log line draws its time in code, who did it in whoOfLog's words, what was done, and its words under it", async () => {
  const record = design();
  const get = served(record, {
    log: [
      logLine(1n, at("@13:50"), "conversation", "asked", ["Q1"]),
      logLine(2n, at("@13:52"), "owner", "said", ["Q1"], "Which keeps the Lisbon case?"),
      logLine(3n, at("@13:56"), "orreryd", "recorded", []),
    ],
  });
  await withPage({ design: record, get }, (container) => {
    const lines = [...sectionNamed(container, "Log").querySelectorAll<HTMLElement>("li.design-log-line")];
    assert.equal(lines.length, 3, "a log line is not drawn");
    assert.deepEqual(
      lines.map((line) => line.querySelector("code")?.textContent),
      [at("@13:50"), at("@13:52"), at("@13:56")].map((time) => noticeClock(time)),
      "a log line's time is not drawn in code",
    );
    assert.deepEqual(
      lines.map((line) => line.querySelector(".design-log-who")?.textContent),
      ["the Director", "you", "orreryd"],
      "a log line's maker is not named through whoOfLog",
    );
    assert.deepEqual(
      lines.map((line) => line.querySelector(".design-log-what")?.textContent),
      ["asked", "said", "recorded"],
      "a log line's what is not drawn",
    );
    assert.equal(lines[1]?.querySelector(".design-log-words")?.textContent, "Which keeps the Lisbon case?", "a line's words are not drawn under it");
    assertNone(lines[2]?.querySelector(".design-log-words"), "a line with no words draws a words line");
  });
});

test("DesignPage: approaches mark the recommended one and the chosen one, and name the chooser through whoOfMaker", async () => {
  const record = design();
  const get = served(record, {
    approaches: [
      create(DesignApproachSchema, { seq: 1n, name: "Pin the database", recommended: true, shape: "Keep the vendored copy." }),
      create(DesignApproachSchema, { seq: 2n, name: "Read the system database", shape: "Read the host's copy." }),
    ],
    choice: create(DesignChoiceSchema, {
      approach: 2n,
      words: "The second one.",
      maker: makerOf({ kind: "owner_conversation", quote: "let's keep the second one" }),
    }),
  });
  await withPage({ design: record, get }, (container) => {
    const first = container.querySelector<HTMLElement>('[data-approach="1"]');
    const second = container.querySelector<HTMLElement>('[data-approach="2"]');
    assert.ok(first?.classList.contains("recommended") && !first.classList.contains("chosen"), "the recommended approach is not marked");
    assert.ok(second?.classList.contains("chosen") && !second.classList.contains("recommended"), "the chosen approach is not marked");
    assert.equal(
      container.querySelector(".design-choice > span:nth-child(2)")?.textContent,
      "you, in the conversation, quoting “let's keep the second one”",
      "the choice's maker is not named through whoOfMaker",
    );
  });
});

test("DesignPage: the contracts draw through the design card, and a press on one opens its contract by its id", async () => {
  const opened: string[] = [];
  const record = design([contract("invented/first", "accepted", 1n), contract("invented/second", "proposed", 2n)]);
  await withPage({ design: record, get: served(record), onOpenContract: (id) => opened.push(id) }, async (container) => {
    assert.equal(
      sectionNamed(container, "Contracts").querySelectorAll("ol[aria-label='contracts'] li").length,
      2,
      "the contracts are not drawn in the design card",
    );
    const first = [...container.querySelectorAll<HTMLButtonElement>("button.design-card-contract")].find(
      (button) => button.querySelector(".design-card-title")?.textContent === "first",
    );
    assert.ok(first, "the first contract's row is not drawn");
    await act(() => {
      first.click();
    });
    assert.deepEqual(opened, ["invented/first"], "a press on a contract does not open it by its id");
  });
});

test("DesignPage: a record with no stage draws the stage record is unavailable where the strip is, and no steelman, questions or research", async () => {
  const record = design();
  const get = served(record, { log: [logLine(1n, at("@13:50"), "owner", "said", [], "Hold the rest.")] });
  await withPage({ design: record, get }, (container) => {
    assert.equal(container.querySelector("p.inote")?.textContent, "the stage record is unavailable", "the unavailable line is not drawn");
    assertNone(container.querySelector("nav"), "the strip is drawn without a stage");
    assertNone(container.querySelector('section[aria-label="the steelman"]'), "the steelman is drawn without a stage");
    assertNone(container.querySelector('section[aria-label="Questions"]'), "the questions are drawn without a stage");
    assertNone(container.querySelector('section[aria-label="Research"]'), "the research is drawn without a stage");
    assert.ok(container.querySelector('section[aria-label="Log"]'), "the log is not drawn, though the record holds it");
  });
});

test("DesignPage: an error draws the error line where the stage sections are, and no stage section", async () => {
  const record = design();
  const get = served(record, { stage: { steps: [research("step-1", "the drafts", "")] } });
  await withPage({ design: record, get, error: "the design did not load: orreryd is not running" }, (container) => {
    assert.equal(
      container.querySelector('p.inote.error[role="alert"]')?.textContent,
      "the design did not load: orreryd is not running",
      "the error line is not drawn",
    );
    assertNone(container.querySelector("nav"), "the strip is drawn beside an error");
    assertNone(container.querySelector('section[aria-label="Research"]'), "the research is drawn beside an error");
  });
});

test("DesignPage: a record still loading draws the loading line in the strip's place, and no stage section", async () => {
  await withPage({ design: design(), get: null, error: "" }, (container) => {
    assert.equal(container.querySelector("p.inote")?.textContent, "the design is loading", "the loading line is not drawn");
    assertNone(container.querySelector("nav"), "the strip is drawn while the record loads");
    assertNone(container.querySelector('section[aria-label="Questions"]'), "the questions are drawn while the record loads");
  });
});

test("DesignPage: a design whose record holds only an empty stage draws its strip at Research and no section", async () => {
  const record = design();
  await withPage({ design: record, get: served(record, { stage: {} }) }, (container) => {
    assert.equal(currentStage(container), "Research", "the strip does not mark Research for an empty stage");
    assert.equal(container.querySelectorAll("section").length, 0, "a part the record does not hold is drawn as a section");
  });
});
