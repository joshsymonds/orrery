import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { render } from "preact";
import { act } from "preact/test-utils";

import type { Conversation } from "./conversation";
import {
  type EpicsGetResponse,
  EpicsGetResponseSchema,
  type EpicStep,
  EpicStepSchema,
} from "./gen/orrery/v1/epics_pb";
import { ConversationItemSchema } from "./gen/orrery/v1/orrery_pb";
import { PhaseOutputPage } from "./phaseoutput";
import { installTestDom, removeTestDom } from "./testdom";

type Props = Parameters<typeof PhaseOutputPage>[0];
type StepInit = MessageInitShape<typeof EpicStepSchema>;

// now is the fixed instant every test draws at, in unix seconds.
const now = 1000;

// A step of task T1 that runs in stage turn from second 100, unless the init says otherwise.
function step(init: StepInit): EpicStep {
  return create(EpicStepSchema, {
    task: "T1",
    agent: "builder",
    role: "implementer",
    doing: "",
    held: false,
    end: "",
    stage: "turn",
    started: 100n,
    ...init,
  });
}

// The attempts of T1, alpha: the first ended done, the second spent on its checks, the third done. The third's last round
// fails its lint and passes its gate, and its judge meets the goal.
function attemptSteps(): EpicStep[] {
  return [
    step({
      id: "impl1",
      end: "done",
      started: 100n,
      ended: 120n,
      doing: "alpha writing the parser",
      summary: "alpha wrote the parser",
    }),
    step({
      id: "impl2",
      end: "checks_failed",
      started: 130n,
      ended: 160n,
      reason: "alpha checks failed",
      summary: "alpha checks ran",
      gateName: "make-gate",
      checks: [
        { round: 1, name: "fmt", ok: true, detail: "" },
        { round: 1, name: "lint", ok: false, detail: "alpha lint detail" },
        {
          round: 1,
          name: "gate",
          ok: false,
          detail: "alpha gate detail",
          tailStdout: "alpha out",
          tailStderr: "alpha err",
        },
      ],
      verdicts: [{ round: 1, verdict: "unmet", reason: "alpha judge reason one" }],
    }),
    step({
      id: "impl3",
      end: "done",
      started: 170n,
      ended: 200n,
      summary: "alpha fixed the lint",
      gateName: "make-gate",
      checks: [
        { round: 2, name: "fmt", ok: true, detail: "" },
        { round: 2, name: "lint", ok: false, detail: "alpha lint again" },
        { round: 2, name: "gate", ok: true, detail: "alpha round two gate", tailStdout: "round two out" },
      ],
      verdicts: [{ round: 2, verdict: "met", reason: "alpha judge reason two" }],
    }),
  ];
}

// The review of T1 that runs now, from second 210, its stage started at second 220, held or not.
function reviewStep(held: boolean): EpicStep {
  return step({
    id: "rev1",
    role: "reviewer",
    readOnly: true,
    end: "",
    held,
    started: 210n,
    stage: "review",
    stageStarted: 220n,
    doing: "alpha reviewing the parser",
  });
}

// The steps of T2, beta: one attempt and one review, both ended.
function betaSteps(): EpicStep[] {
  return [
    step({
      id: "b1",
      task: "T2",
      end: "done",
      started: 100n,
      ended: 110n,
      summary: "beta printed the output",
    }),
    step({
      id: "b2",
      task: "T2",
      role: "reviewer",
      readOnly: true,
      end: "done",
      started: 120n,
      ended: 130n,
      summary: "beta reviewed the printer",
    }),
  ];
}

// The Get answer of an epic with T1 Parser in review, running rev1 (held or not), T2 Printer integrated, and T3 Logout
// planned. T1's red and green runs have ended and passed, and its review has one confirmed finding.
function detailOf(held: boolean): EpicsGetResponse {
  return create(EpicsGetResponseSchema, {
    epic: { slug: "orrery-style", outcome: "accepted" },
    buildTasks: [
      {
        id: "T1",
        title: "Parser",
        status: "dispatched",
        stage: "review",
        requirements: ["R1"],
        red: {
          id: "red-alpha",
          phase: "red",
          source: "captured",
          state: "ended",
          exit: 1,
          passed: true,
          verdict: "holds",
          created: 100n,
          ended: 145n,
          revision: "alpha-red-rev",
        },
        green: {
          id: "green-alpha",
          phase: "green",
          source: "captured",
          state: "ended",
          exit: 0,
          passed: true,
          verdict: "holds",
          created: 500n,
          ended: 530n,
          revision: "alpha-green-rev",
        },
        reviews: [
          {
            attempt: 1n,
            gate: "alpha review gate",
            step: "rev1",
            verdict: "fails",
            findings: [
              {
                id: "f1",
                claim: "alpha claim",
                location: "parser.go",
                step: "rev1",
                result: "confirmed",
                reason: "alpha finding reason",
              },
            ],
          },
        ],
      },
      {
        id: "T2",
        title: "Printer",
        status: "done",
        requirements: ["R2", "R3"],
        reviews: [{ attempt: 1n, gate: "beta review gate", step: "b2", verdict: "holds", findings: [] }],
      },
      { id: "T3", title: "Logout", status: "", requirements: ["R2"] },
    ],
    requirements: [
      { id: "R1", text: "The parser parses" },
      { id: "R2", text: "The printer prints" },
    ],
    steps: [...attemptSteps(), reviewStep(held), ...betaSteps()],
  });
}

// briefConversation is a conversation whose first brief prompt is the text given, after an assistant item.
function briefConversation(text: string): Conversation {
  return {
    transcript: "",
    seq: 0n,
    items: [
      create(ConversationItemSchema, { kind: "assistant", text: "reading the repository" }),
      create(ConversationItemSchema, { kind: "prompt", sender: "brief", text }),
      create(ConversationItemSchema, { kind: "prompt", sender: "brief", text: "a second brief" }),
    ],
    oldest: 0n,
    more: false,
    state: undefined,
    unfinished: [],
    queued: [],
    unsent: [],
  };
}

// pageProps is the page's props for a task, with every callback recording its call as a string in calls.
function pageProps(detail: EpicsGetResponse, task: string, calls: string[], init: Partial<Props> = {}): Props {
  return {
    detail,
    task,
    now,
    narrow: false,
    conversation: null,
    loadArtifact: () => Promise.reject(new Error("the page loads no artifact in these tests")),
    onBack: () => {
      calls.push("back");
    },
    onOpenEpic: () => {
      calls.push("epic");
    },
    onOpenTask: (openedTask, phase) => {
      calls.push(`task:${openedTask}:${phase}`);
    },
    onOpenSession: (session) => {
      calls.push(`session:${session}`);
    },
    hold: (session) => {
      calls.push(`hold:${session}`);
      return Promise.resolve();
    },
    stop: (session) => {
      calls.push(`stop:${session}`);
      return Promise.resolve();
    },
    ...init,
  };
}

// withPage renders the page into a fresh test DOM, runs the check on its container, and removes the DOM.
async function withPage(props: Props, check: (container: HTMLElement) => Promise<void> | void): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  try {
    await act(() => {
      render(<PhaseOutputPage {...props} />, container);
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

// click dispatches a click on the element and lets the call it starts settle.
async function click(element: Element): Promise<void> {
  await act(async () => {
    element.dispatchEvent(new Event("click", { bubbles: true }));
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
}

function elementOf<T extends Element>(root: ParentNode, selector: string): T {
  const found = root.querySelector<T>(selector);
  assert.ok(found, `the page has ${selector}`);
  return found;
}

function textsOf(root: ParentNode, selector: string): string[] {
  return [...root.querySelectorAll(selector)].map((element) => element.textContent ?? "");
}

function buttonNamed(root: ParentNode, name: string): HTMLButtonElement | undefined {
  return [...root.querySelectorAll<HTMLButtonElement>("button")].find((button) => button.textContent === name);
}

function buttonOf(root: ParentNode, name: string): HTMLButtonElement {
  const found = buttonNamed(root, name);
  assert.ok(found, `the page has a ${name} button`);
  return found;
}

// stageHead is the head of the stage row named name.
function stageHead(container: HTMLElement, name: string): HTMLButtonElement {
  const head = [...container.querySelectorAll<HTMLButtonElement>(".phase-head")].find(
    (candidate) => candidate.querySelector(".nm")?.textContent === name,
  );
  assert.ok(head, `the page has a ${name} stage head`);
  return head;
}

// openStagesOf names the stages whose heads are expanded, joined by commas.
function openStagesOf(container: HTMLElement): string {
  return textsOf(container, '.phase-head[aria-expanded="true"] .nm').join(",");
}

// bodyOf is the text of the open stage's body, or empty when no stage is open.
function bodyOf(container: HTMLElement): string {
  return container.querySelector(".phase-body")?.textContent ?? "";
}

// commandsOf is the text of the commands the title row draws, in order.
function titleCommandsOf(container: HTMLElement): string[] {
  return textsOf(container, ".title-row .acts > *");
}

test("TaskBand: the crumbs read the epic, then the task, and both the epic crumb and ‹ leave the page", async () => {
  const calls: string[] = [];
  await withPage(pageProps(detailOf(false), "T1", calls), async (container) => {
    assert.equal(elementOf(container, ".crumbs").textContent?.trim(), "› orrery-style › Parser");
    assert.equal(elementOf(container, ".crumbs .here").textContent, "Parser");
    await click(elementOf(container, ".back"));
    const [epic] = [...container.querySelectorAll<HTMLButtonElement>(".crumbs button.crumb")];
    assert.ok(epic, "the epic crumb is a button");
    await click(epic);
    assert.equal(calls.join(","), "back,epic");
  });
});

test("TaskBand: the epic pin names the epic, its state and its tasks done, and pressing it opens the epic", async () => {
  const calls: string[] = [];
  await withPage(pageProps(detailOf(false), "T1", calls), async (container) => {
    assert.equal(elementOf(container, ".epic-pin .slug").textContent, "orrery-style");
    assert.equal(elementOf(container, ".epic-pin .words").textContent, "accepted · 1 of 3 tasks done");
    await click(elementOf(container, ".epic-pin"));
    assert.equal(calls.join(","), "epic");
  });
});

test("TaskBand: the task strip marks the current task's chip and opens every other task at its place's phase", async () => {
  const calls: string[] = [];
  await withPage(pageProps(detailOf(false), "T1", calls), async (container) => {
    const chips = [...container.querySelectorAll<HTMLButtonElement>(".tstrip .tchip")];
    assert.equal(chips.map((chip) => chip.querySelector(".tt")?.textContent).join(","), "Parser,Printer,Logout");
    assert.equal(chips[0].getAttribute("aria-current"), "page");
    assert.equal(chips[0].classList.contains("cur"), true);
    await click(chips[0]);
    await click(chips[1]);
    await click(chips[2]);
    assert.equal(calls.join(","), "task:T2:integrating,task:T3:implementing");
  });
});

test("TaskBand: each requirement the task proves is a line with its text, or its id alone when it is not listed", async () => {
  await withPage(pageProps(detailOf(false), "T1", []), (container) => {
    assert.deepEqual(textsOf(container, ".now-sentence"), ["R1 The parser parses"]);
  });
  await withPage(pageProps(detailOf(false), "T2", []), (container) => {
    assert.deepEqual(textsOf(container, ".now-sentence"), ["R2 The printer prints", "R3"]);
    assert.equal(elementOf(container, ".now-sentence .id").textContent, "R2");
  });
});

test("TaskBand: the planner's brief is drawn from the first brief prompt, and not before its conversation holds one", async () => {
  await withPage(pageProps(detailOf(false), "T1", []), (container) => {
    assert.equal(container.querySelector(".brief"), null, "no brief while its conversation is not held");
  });
  await withPage(pageProps(detailOf(false), "T1", [], { conversation: briefConversation("Parse the lines.") }), (container) => {
    assert.equal(elementOf(container, ".brief .lbl").textContent, "The planner’s brief");
    assert.equal(elementOf(container, ".brief p").textContent, "Parse the lines.");
  });
});

test("TaskStages: five rows in order, each with its state, its summary and its time", async () => {
  await withPage(pageProps(detailOf(false), "T1", []), (container) => {
    assert.equal(textsOf(container, ".phase .nm").join(","), "red,implement,green,review,integrate");
    assert.equal(
      [...container.querySelectorAll(".phase")].map((row) => row.className).join("|"),
      "phase passed|phase passed|phase passed|phase now run|phase ahead",
    );
    assert.equal(
      textsOf(container, ".phase .sum").join("|"),
      "the test fails before any change · judged to hold|attempt 3 · the gate passed · judged met|the test passes on the change · judged to hold|alpha review gate: 1 finding, 1 confirmed|not reached",
    );
    assert.equal(textsOf(container, ".phase .end").join(","), "45 s,1 min,30 s,13:00,");
  });
});

test("TaskStages: the record opens on the now row, a passed head opens in place and closes it, and a second press folds it", async () => {
  await withPage(pageProps(detailOf(false), "T1", []), async (container) => {
    assert.equal(openStagesOf(container), "review");
    await click(stageHead(container, "red"));
    assert.equal(openStagesOf(container), "red", "the red row opens and the review row closes");
    await click(stageHead(container, "red"));
    assert.equal(openStagesOf(container), "", "a press on the open head folds it");
  });
});

test("TaskStages: an ahead row is disabled and opens nothing, and a planned task opens no row", async () => {
  await withPage(pageProps(detailOf(false), "T1", []), async (container) => {
    const integrate = stageHead(container, "integrate");
    assert.equal(integrate.disabled, true);
    await click(integrate);
    assert.equal(openStagesOf(container), "review");
  });
  await withPage(pageProps(detailOf(false), "T3", []), (container) => {
    assert.equal(openStagesOf(container), "");
    assert.equal(
      [...container.querySelectorAll<HTMLButtonElement>(".phase-head")].every((head) => head.disabled),
      true,
    );
  });
});

test("TaskStages: an integrated task opens its integrate row, which reads Merged", async () => {
  await withPage(pageProps(detailOf(false), "T2", []), (container) => {
    assert.equal(openStagesOf(container), "integrate");
    assert.equal(bodyOf(container), "Merged.");
  });
});

test("TaskStages: the red body draws the red run, and a task with no red run says so", async () => {
  await withPage(pageProps(detailOf(false), "T1", []), async (container) => {
    await click(stageHead(container, "red"));
    assert.equal(
      elementOf(container, ".phase-body .run .sentence").textContent,
      "Red run: the test fails before any change, as it should.",
    );
  });
  await withPage(pageProps(detailOf(false), "T2", []), async (container) => {
    await click(stageHead(container, "red"));
    assert.equal(bodyOf(container), "No red run yet.");
  });
});

test("TaskStages: the implement body lists the attempts, the latest attempt's rounds, its gate log and its judge", async () => {
  await withPage(pageProps(detailOf(false), "T1", []), async (container) => {
    await click(stageHead(container, "implement"));
    assert.equal(
      textsOf(container, ".phase-body .sub").join(","),
      "Attempts,Attempt 3 · check rounds,Gate · make-gate,Judge",
    );
    assert.equal(textsOf(container, ".phase-body .attempt .words").join("|"), "alpha wrote the parser|alpha checks ran|alpha fixed the lint");
    assert.equal(textsOf(container, ".phase-body .attempt .chip").join(","), "done,spent,done");
    assert.equal(textsOf(container, '.phase-body .reader.check-rounds [data-shape="block"].checks .chip').join(","), "fmt,lint,gate");
    assert.equal(textsOf(container, '.phase-body .reader.check-rounds [data-shape="block"].checks .chip.bad').join(","), "lint");
    assert.equal(elementOf(container, '.phase-body .reader.check-rounds [data-shape="block"].checks > p').textContent, "lint: alpha lint again");
    assert.equal(elementOf(container, ".phase-body .log").textContent, "round two out");
    assert.equal(elementOf(container, ".phase-body .inote").textContent, "Judged: the goal is met.");
  });
});

test("TaskStages: the green body says what the red test does on the change, and draws the green run", async () => {
  await withPage(pageProps(detailOf(false), "T1", []), async (container) => {
    await click(stageHead(container, "green"));
    assert.equal(textsOf(container, ".phase-body .sub").join(","), "Reproduction,Green run");
    assert.equal(elementOf(container, ".phase-body .inote").textContent, "The red test, run on the change, now passes.");
    assert.equal(elementOf(container, ".phase-body .run .sentence").textContent, "Green run: the test passes on the change.");
  });
});

test("TaskStages: the review body is a table of each finding, and says when no review has run", async () => {
  await withPage(pageProps(detailOf(false), "T1", []), (container) => {
    assert.equal(textsOf(container, ".phase-body .tbl .gate").join(","), "alpha review gate");
    assert.equal(textsOf(container, ".phase-body .tbl .what").join(","), "alpha claim");
    assert.equal(elementOf(container, ".phase-body .tbl .id").textContent, "parser.go");
    assert.equal(elementOf(container, ".phase-body .tbl .chip").textContent, "confirmed");
    assert.equal(elementOf(container, ".phase-body .tbl .res").textContent?.includes("alpha finding reason"), true);
  });
  const unreviewed = detailOf(false);
  const [parser] = unreviewed.buildTasks;
  parser.reviews = [];
  await withPage(pageProps(unreviewed, "T1", []), async (container) => {
    assert.equal(openStagesOf(container), "review");
    assert.equal(elementOf(container, ".phase-body .inote").textContent, "No review has run yet.");
  });
});

test("TaskStages: the review table's head row names its four columns, and its finding and what-happened cells are wide", async () => {
  await withPage(pageProps(detailOf(false), "T1", []), (container) => {
    assert.equal(textsOf(container, ".phase-body .tbl .tr.head > span").join(","), "gate,finding,where,what happened");
    assert.equal(elementOf(container, ".phase-body .tbl .tr:not(.head) > .what").classList.contains("wide"), true);
    assert.equal(elementOf(container, ".phase-body .tbl .tr:not(.head) > .res").classList.contains("wide"), true);
  });
});

test("TaskStages: the session door of implement opens its latest attempt, and of review its latest reading step", async () => {
  const calls: string[] = [];
  await withPage(pageProps(detailOf(false), "T1", calls), async (container) => {
    await click(elementOf(container, ".phase-body .door"));
    await click(stageHead(container, "implement"));
    await click(elementOf(container, ".phase-body .door"));
    assert.equal(calls.join(","), "session:rev1,session:impl3");
  });
});

test("TaskCommands: Interrupt sends one hold of the running step, and a held step offers no Interrupt", async () => {
  const calls: string[] = [];
  await withPage(pageProps(detailOf(false), "T1", calls), async (container) => {
    assert.deepEqual(titleCommandsOf(container), ["Interrupt", "Stop…"]);
    await click(buttonOf(container, "Interrupt"));
    assert.equal(calls.join(","), "hold:rev1");
  });
  await withPage(pageProps(detailOf(true), "T1", []), (container) => {
    assert.deepEqual(titleCommandsOf(container), ["Stop…"]);
  });
});

test("TaskCommands: with no running step there are no commands", async () => {
  await withPage(pageProps(detailOf(false), "T2", []), (container) => {
    assert.deepEqual(titleCommandsOf(container), []);
  });
});

test("TaskCommands: Stop… asks first, Keep it running sends nothing, and the confirm sends one stop of the running step", async () => {
  const calls: string[] = [];
  await withPage(pageProps(detailOf(false), "T1", calls), async (container) => {
    await click(buttonOf(container, "Stop…"));
    assert.equal(elementOf(container, ".title-row .sure").textContent, "Stop Parser?");
    assert.equal(calls.join(","), "", "asking sends nothing");
    await click(buttonOf(container, "Keep it running"));
    assert.equal(calls.join(","), "");
    await click(buttonOf(container, "Stop…"));
    await click(buttonOf(container, "Stop Parser"));
    assert.equal(calls.join(","), "stop:rev1");
    assert.deepEqual(titleCommandsOf(container), ["Interrupt", "Stop…"], "the commands return after the confirm");
  });
});

test("TaskCommands: a refused Interrupt shows its line under the title row, and a later call that lands clears it", async () => {
  let refuse = true;
  const hold = () =>
    refuse ? Promise.reject(new ConnectError("step is done", Code.FailedPrecondition)) : Promise.resolve();
  await withPage(pageProps(detailOf(false), "T1", [], { hold }), async (container) => {
    await click(buttonOf(container, "Interrupt"));
    assert.equal(elementOf(container, ".title-row + .page-note").textContent, "Interrupt was refused: step is done.");
    refuse = false;
    await click(buttonOf(container, "Interrupt"));
    assert.equal(container.querySelector(".page-note"), null, "a call that lands clears the line");
  });
});

test("TaskCommands: a refused Stop names its reason, and an unknown outcome says it may have landed", async () => {
  const stop = () => Promise.reject(new ConnectError("no such step", Code.NotFound));
  await withPage(pageProps(detailOf(false), "T1", [], { stop }), async (container) => {
    await click(buttonOf(container, "Stop…"));
    await click(buttonOf(container, "Stop Parser"));
    assert.equal(elementOf(container, ".page-note").textContent, "Stop was refused: no such step.");
  });
  const unknown = () => Promise.reject(new ConnectError("try later", Code.Unavailable));
  await withPage(pageProps(detailOf(false), "T1", [], { hold: unknown }), async (container) => {
    await click(buttonOf(container, "Interrupt"));
    assert.equal(elementOf(container, ".page-note").textContent, "Interrupt may have landed: no answer came.");
  });
});

test("TaskCommands: the state pill reads the place and the clock while a step runs, else the task's words", async () => {
  await withPage(pageProps(detailOf(false), "T1", []), (container) => {
    assert.equal(elementOf(container, ".title-row .state").textContent, "review · 13:00");
    assert.equal(elementOf(container, ".title-row .state").classList.contains("t-live"), true);
  });
  await withPage(pageProps(detailOf(false), "T2", []), (container) => {
    assert.equal(elementOf(container, ".title-row .state").textContent, "done");
  });
  await withPage(pageProps(detailOf(false), "T3", []), (container) => {
    assert.equal(elementOf(container, ".title-row .state").textContent, "not started");
  });
});

test("TaskCommands: narrow, the commands leave the title row for a foot bar, and More opens the confirm sheet", async () => {
  const calls: string[] = [];
  await withPage(pageProps(detailOf(false), "T1", calls, { narrow: true }), async (container) => {
    assert.deepEqual(titleCommandsOf(container), [], "the title row holds no commands");
    assert.equal(elementOf(container, ".cmdbar .state").textContent, "review · 13:00");
    await click(buttonOf(container, "Interrupt"));
    assert.equal(calls.join(","), "hold:rev1");
    await click(elementOf(container, '.cmdbar button[aria-label="Stop this step…"]'));
    const sheet = elementOf<HTMLElement>(container, '[role="dialog"]');
    assert.equal(elementOf(sheet, ".sheet-head b").textContent, "Stop Parser?");
    assert.equal(sheet.textContent?.includes("The task ends short and the planner decides what to do next."), true);
    await click(buttonOf(sheet, "Keep it running"));
    assert.equal(container.querySelector('[role="dialog"]'), null);
    assert.equal(calls.join(","), "hold:rev1", "Keep it running sends nothing");
    await click(elementOf(container, '.cmdbar button[aria-label="Stop this step…"]'));
    await click(buttonOf(elementOf(container, '[role="dialog"]'), "Stop Parser"));
    assert.equal(calls.join(","), "hold:rev1,stop:rev1");
    assert.equal(container.querySelector('[role="dialog"]'), null, "the sheet closes once confirmed");
  });
});
