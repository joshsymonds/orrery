import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { render } from "preact";
import { act } from "preact/test-utils";

import { type EpicsGetResponse, EpicsGetResponseSchema, type EpicStep, EpicStepSchema } from "./gen/orrery/v1/epics_pb";
import { PhaseOutputPage } from "./phaseoutput";
import { type Phase, PHASES } from "./phases";
import { installTestDom, removeTestDom } from "./testdom";

type Props = Parameters<typeof PhaseOutputPage>[0];
type StepInit = MessageInitShape<typeof EpicStepSchema>;

// A step of task T1 that runs in stage turn from second 100, unless the init says otherwise.
function step(init: StepInit): EpicStep {
  return create(EpicStepSchema, {
    task: "T1",
    agent: "builder",
    doing: "",
    held: false,
    end: "",
    stage: "turn",
    started: 100n,
    ...init,
  });
}

// The steps of T1, alpha: an attempt that ended, a checks round that failed, a second attempt whose checks passed, and a
// review. The last step is the review, so the task waits to integrate.
function alphaSteps(): EpicStep[] {
  return [
    step({
      id: "a1",
      end: "done",
      started: 100n,
      ended: 110n,
      doing: "alpha writing the parser",
      summary: "alpha wrote the parser",
    }),
    step({
      id: "c1",
      end: "checks_failed",
      started: 120n,
      ended: 150n,
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
      id: "c2",
      end: "done",
      started: 160n,
      ended: 170n,
      summary: "alpha fixed the lint",
      gateName: "make-gate",
      checks: [
        { round: 2, name: "fmt", ok: true, detail: "" },
        { round: 2, name: "gate", ok: true, detail: "alpha round two gate" },
      ],
      verdicts: [{ round: 2, verdict: "met", reason: "alpha judge reason two" }],
    }),
    step({
      id: "r1",
      readOnly: true,
      end: "done",
      started: 180n,
      ended: 200n,
      doing: "alpha reviewing the parser",
      summary: "alpha reviewed the parser",
    }),
  ];
}

// The steps of T2, beta: one attempt and one review, both ended.
function betaSteps(): EpicStep[] {
  return [
    step({ id: "b1", task: "T2", end: "done", started: 100n, ended: 110n, summary: "beta printed the output" }),
    step({
      id: "b2",
      task: "T2",
      readOnly: true,
      end: "done",
      started: 120n,
      ended: 130n,
      summary: "beta reviewed the printer",
    }),
  ];
}

// The step of T1 that runs now, after its history, held or not. Its stage started at second 400.
function runningStep(held: boolean): EpicStep {
  return step({
    id: "run1",
    end: "",
    held,
    started: 200n,
    stageStarted: 400n,
    doing: "alpha writing the parser again",
  });
}

// The Get answer of an epic with task T1 (Parser, open) and task T2 (Printer, done), with the given steps.
function detailOf(steps: EpicStep[]): EpicsGetResponse {
  return create(EpicsGetResponseSchema, {
    buildTasks: [
      {
        id: "T1",
        title: "Parser",
        status: "open",
        stage: "green",
        requirements: ["R1"],
        red: {
          id: "red-alpha",
          phase: "red",
          source: "captured",
          state: "ended",
          exit: 1,
          passed: false,
          revision: "alpha-red-rev",
        },
        green: {
          id: "green-alpha",
          phase: "green",
          source: "captured",
          state: "ended",
          exit: 0,
          passed: true,
          revision: "alpha-green-rev",
        },
        reviews: [
          {
            attempt: 1n,
            gate: "alpha review gate",
            step: "r1",
            verdict: "fails",
            findings: [
              {
                id: "f1",
                claim: "alpha claim",
                location: "parser.go",
                step: "r1",
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
        stage: "verify",
        requirements: ["R2", "R3"],
        reviews: [{ attempt: 1n, gate: "beta review gate", step: "b2", verdict: "holds", findings: [] }],
      },
    ],
    steps,
  });
}

// The epic with T1 waiting to integrate.
function finished(): EpicsGetResponse {
  return detailOf([...alphaSteps(), ...betaSteps()]);
}

// The epic with T1 running a step, held or not.
function running(held: boolean): EpicsGetResponse {
  return detailOf([...alphaSteps(), runningStep(held), ...betaSteps()]);
}

// pageProps is the page's props for a task at a phase, with every callback recording its call as a string in calls.
function pageProps(
  detail: EpicsGetResponse,
  task: string,
  phase: Phase,
  calls: string[],
  init: Partial<Props> = {},
): Props {
  return {
    detail,
    epic: "orrery-style",
    task,
    phase,
    now: 1000,
    loadArtifact: () => Promise.reject(new Error("the page loads no artifact in these tests")),
    onBack: () => {
      calls.push("back");
    },
    onGoList: () => {
      calls.push("list");
    },
    onOpenPhase: (openedTask, openedPhase) => {
      calls.push(`phase:${openedTask}:${openedPhase}`);
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

function bodyOf(container: HTMLElement): string {
  return container.querySelector(".phase-body")?.textContent ?? "";
}

function buttonNamed(root: ParentNode, name: string): HTMLButtonElement | undefined {
  return [...root.querySelectorAll<HTMLButtonElement>("button")].find((button) => button.textContent === name);
}

function buttonOf(root: ParentNode, name: string): HTMLButtonElement {
  const found = buttonNamed(root, name);
  assert.ok(found, `the page has a ${name} button`);
  return found;
}

function headOf(container: HTMLElement, phase: Phase): HTMLButtonElement {
  const head = [...container.querySelectorAll<HTMLButtonElement>(".phase-head")].find(
    (candidate) => candidate.querySelector(".phase-name")?.textContent === phase,
  );
  assert.ok(head, `the page has a ${phase} head`);
  return head;
}

// openPhasesOf names the phases of the open rows, joined by commas.
function openPhasesOf(container: HTMLElement): string {
  return textsOf(container, ".phase-row.open .phase-name").join(",");
}

test("the crumbs read orrery, the epic, then the task and phase, with the phase as the page", async () => {
  await withPage(pageProps(finished(), "T1", "checking", []), (container) => {
    assert.equal(elementOf(container, ".crumbs").textContent, "orrery › orrery-style › T1 · checking");
    assert.equal(elementOf(container, ".crumbs .here").textContent, "T1 · checking");
  });
});

test("‹ calls onBack, and the orrery and epic crumbs call onGoList and onBack", async () => {
  const calls: string[] = [];
  await withPage(pageProps(finished(), "T1", "checking", calls), async (container) => {
    await click(elementOf(container, ".back"));
    const crumbs = [...container.querySelectorAll<HTMLButtonElement>(".crumbs button.crumb")];
    const [orrery, epic] = crumbs;
    assert.ok(orrery && epic, "the orrery and epic crumbs are buttons");
    await click(orrery);
    await click(epic);
    assert.equal(calls.join(","), "back,list,back");
  });
});

test("the page names the task's title and its requirement ids", async () => {
  await withPage(pageProps(finished(), "T1", "implementing", []), (container) => {
    assert.equal(elementOf(container, ".task-title").textContent, "Parser");
    assert.equal(elementOf(container, ".requirements").textContent, "R1");
  });
  await withPage(pageProps(finished(), "T2", "implementing", []), (container) => {
    assert.equal(elementOf(container, ".task-title").textContent, "Printer");
    assert.equal(elementOf(container, ".requirements").textContent, "R2, R3");
  });
});

test("the four phases are rows in order, each named by its phase", async () => {
  await withPage(pageProps(finished(), "T1", "implementing", []), (container) => {
    assert.equal(textsOf(container, ".phase-row .phase-name").join(","), PHASES.join(","));
  });
});

test("given each phase, that row alone is open and its head shows it expanded", async () => {
  for (const phase of PHASES) {
    await withPage(pageProps(finished(), "T1", phase, []), (container) => {
      assert.equal(openPhasesOf(container), phase);
      const expanded = [...container.querySelectorAll(".phase-head")].map((head) => head.getAttribute("aria-expanded"));
      assert.equal(expanded.join(","), PHASES.map((name) => String(name === phase)).join(","));
    });
  }
});

test("another head calls onOpenPhase with its task and phase", async () => {
  const calls: string[] = [];
  await withPage(pageProps(finished(), "T1", "checking", calls), async (container) => {
    await click(headOf(container, "implementing"));
    await click(headOf(container, "reviewing"));
    await click(headOf(container, "integrating"));
    assert.equal(calls.join(","), "phase:T1:implementing,phase:T1:reviewing,phase:T1:integrating");
  });
});

test("a head ahead of the work is disabled, and the others are not", async () => {
  await withPage(pageProps(running(false), "T1", "implementing", []), (container) => {
    const heads = [...container.querySelectorAll<HTMLButtonElement>(".phase-head")];
    assert.equal(heads.map((head) => String(head.disabled)).join(","), "false,true,true,true");
  });
});

test("each row's head shows its summary, and a passed row its seconds, a now row its time, an ahead row none", async () => {
  await withPage(pageProps(finished(), "T1", "implementing", []), (container) => {
    assert.equal(
      textsOf(container, ".phase-summary").join("|"),
      "alpha fixed the lint|alpha checks ran|alpha reviewed the parser|",
    );
    assert.equal(textsOf(container, ".phase-row.passed .phase-time").join(","), "20 s,30 s,20 s");
  });
  await withPage(pageProps(running(false), "T1", "implementing", []), (container) => {
    assert.equal(textsOf(container, ".phase-time").join("|"), "10 min|||");
  });
});

test("the implementing body shows the red run, each attempt, and the green run, and none of the checks or reviews", async () => {
  await withPage(pageProps(finished(), "T1", "implementing", []), (container) => {
    const body = bodyOf(container);
    assert.equal(textsOf(container, ".phase-body .run .phase").join(","), "red,green");
    assert.equal(textsOf(container, ".phase-body .attempt-end").join(","), "done,checks_failed,done");
    assert.equal(
      textsOf(container, ".phase-body .attempt-text").join("|"),
      "alpha wrote the parser|alpha checks failed|alpha fixed the lint",
    );
    assert.equal(body.includes("alpha lint detail"), false);
    assert.equal(body.includes("alpha claim"), false);
  });
});

test("a running attempt in the implementing body shows what it is doing", async () => {
  await withPage(pageProps(running(false), "T1", "implementing", []), (container) => {
    assert.equal(textsOf(container, ".phase-body .attempt-doing").join(","), "alpha writing the parser again");
  });
});

test("the checking body shows each round's ran list, each check's mark, a failed check's detail, the gate and the judge", async () => {
  await withPage(pageProps(finished(), "T1", "checking", []), (container) => {
    const body = bodyOf(container);
    assert.equal(textsOf(container, ".phase-body .round-no").join(","), "round 1,round 2");
    assert.equal(textsOf(container, ".phase-body .ran").join("|"), "checks, gate, judge|checks, gate, judge");
    assert.equal(textsOf(container, ".phase-body .check-name").join(","), "fmt,lint,fmt");
    assert.equal(textsOf(container, ".phase-body .check-result").join(","), "✓,✗,✓");
    assert.equal(textsOf(container, ".phase-body .check-detail").join(","), "alpha lint detail");
    assert.equal(textsOf(container, ".phase-body .gate-name").join(","), "make-gate,make-gate");
    assert.equal(textsOf(container, ".phase-body .gate-result").join(","), "failed,passed");
    assert.equal(textsOf(container, ".phase-body .gate-detail").join(","), "alpha gate detail,alpha round two gate");
    assert.equal(textsOf(container, ".phase-body .gate-stdout").join(","), "alpha out");
    assert.equal(textsOf(container, ".phase-body .gate-stderr").join(","), "alpha err");
    assert.equal(textsOf(container, ".phase-body .judge-verdict").join(","), "unmet,met");
    assert.equal(
      textsOf(container, ".phase-body .judge-reason").join(","),
      "alpha judge reason one,alpha judge reason two",
    );
    assert.equal(body.includes("alpha wrote the parser"), false);
    assert.equal(body.includes("alpha reviewed the parser"), false);
  });
});

test("the reviewing body shows each review's attempt, gate and verdict, each finding, and each reviewer's summary", async () => {
  await withPage(pageProps(finished(), "T1", "reviewing", []), (container) => {
    const body = bodyOf(container);
    assert.equal(textsOf(container, ".phase-body .review-attempt").join(","), "attempt 1");
    assert.equal(textsOf(container, ".phase-body .review-gate").join(","), "alpha review gate");
    assert.equal(textsOf(container, ".phase-body .review-verdict").join(","), "fails");
    assert.equal(textsOf(container, ".phase-body .claim").join(","), "alpha claim");
    assert.equal(textsOf(container, ".phase-body .location").join(","), "parser.go");
    assert.equal(textsOf(container, ".phase-body .result").join(","), "confirmed");
    assert.equal(textsOf(container, ".phase-body .reason").join(","), "alpha finding reason");
    assert.equal(textsOf(container, ".phase-body .reviewer").join(","), "alpha reviewed the parser");
    assert.equal(body.includes("alpha lint detail"), false);
    assert.equal(body.includes("alpha wrote the parser"), false);
  });
});

test("an integrating row that is now shows no time, since it has no start to count from", async () => {
  await withPage(pageProps(finished(), "T1", "implementing", []), (container) => {
    assert.equal(elementOf(container, ".phase-row.now .phase-time").textContent, "");
  });
  await withPage(pageProps(finished(), "T1", "integrating", []), (container) => {
    assert.equal(elementOf(container, ".phase-row.now .phase-time").textContent, "");
  });
});

test("the integrating body says whether the task is integrated or waits to integrate", async () => {
  await withPage(pageProps(finished(), "T1", "integrating", []), (container) => {
    assert.equal(bodyOf(container), "waits to integrate");
  });
  await withPage(pageProps(finished(), "T2", "integrating", []), (container) => {
    assert.equal(bodyOf(container), "integrated");
  });
});

test("Open the session calls onOpenSession with the step the phase names, and an integrating phase names none", async () => {
  const cases: [Phase, string][] = [
    ["implementing", "session:c2"],
    ["checking", "session:c2"],
    ["reviewing", "session:r1"],
  ];
  for (const [phase, expected] of cases) {
    const calls: string[] = [];
    await withPage(pageProps(finished(), "T1", phase, calls), async (container) => {
      await click(buttonOf(elementOf(container, ".phase-body"), "Open the session ›"));
      assert.equal(calls.join(","), expected);
    });
  }
  await withPage(pageProps(finished(), "T1", "integrating", []), (container) => {
    assert.equal(buttonNamed(container, "Open the session ›") === undefined, true);
  });
});

test("Hold calls hold once with the running step", async () => {
  const calls: string[] = [];
  await withPage(pageProps(running(false), "T1", "implementing", calls), async (container) => {
    await click(buttonOf(container, "Hold"));
    assert.equal(calls.join(","), "hold:run1");
  });
});

test("a held step offers no Hold, and Stop… stays", async () => {
  await withPage(pageProps(running(true), "T1", "implementing", []), (container) => {
    assert.equal(buttonNamed(container, "Hold") === undefined, true);
    assert.equal(buttonNamed(container, "Stop…") !== undefined, true);
  });
});

test("with nothing running there is no Hold and no Stop…", async () => {
  await withPage(pageProps(finished(), "T1", "implementing", []), (container) => {
    assert.equal(buttonNamed(container, "Hold") === undefined, true);
    assert.equal(buttonNamed(container, "Stop…") === undefined, true);
  });
});

test("Stop… calls nothing until its confirm button is pressed, and Keep it running calls nothing", async () => {
  const calls: string[] = [];
  await withPage(pageProps(running(false), "T1", "implementing", calls), async (container) => {
    await click(buttonOf(container, "Stop…"));
    assert.equal(calls.join(","), "");
    await click(buttonOf(container, "Keep it running"));
    assert.equal(calls.join(","), "");
    await click(buttonOf(container, "Stop…"));
    assert.equal(calls.join(","), "");
    await click(buttonOf(container, "Stop T1"));
    assert.equal(calls.join(","), "stop:run1");
  });
});

test("a refused hold shows its line", async () => {
  const calls: string[] = [];
  const hold = () => Promise.reject(new ConnectError("step is done", Code.FailedPrecondition));
  await withPage(pageProps(running(false), "T1", "implementing", calls, { hold }), async (container) => {
    await click(buttonOf(container, "Hold"));
    assert.equal(elementOf(container, ".notice").textContent, "hold refused: step is done");
  });
});

test("a hold whose outcome is not known shows the not-known line", async () => {
  const hold = () => Promise.reject(new ConnectError("try later", Code.Unavailable));
  await withPage(pageProps(running(false), "T1", "implementing", [], { hold }), async (container) => {
    await click(buttonOf(container, "Hold"));
    assert.equal(elementOf(container, ".notice").textContent, "hold not known: it may have landed");
  });
});

test("a refused stop shows its line once confirmed", async () => {
  const stop = () => Promise.reject(new ConnectError("no such step", Code.NotFound));
  await withPage(pageProps(running(false), "T1", "implementing", [], { stop }), async (container) => {
    await click(buttonOf(container, "Stop…"));
    await click(buttonOf(container, "Stop T1"));
    assert.equal(elementOf(container, ".notice").textContent, "stop refused: no such step");
  });
});
