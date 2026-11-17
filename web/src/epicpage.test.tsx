import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { type EpicCommands, EpicPage } from "./epicpage";
import type { LoadArtifact } from "./evidence";
import { type EpicsGetResponse, EpicsGetResponseSchema } from "./gen/orrery/v1/epics_pb";
import { installTestDom, removeTestDom } from "./testdom";

type Props = Parameters<typeof EpicPage>[0];

// now is the clock the page measures its times against, in unix seconds.
const now = 10_000;

// liveEpic is a live epic with no outcome, named demo-epic.
const liveEpic = { id: "e1", slug: "demo-epic", repository: "demo-repo", outcome: "", live: true };

// revision is a full commit id; no test here reads it, only that each run has one.
const revision = "0123456789abcdef0123456789abcdef01234567";

// redRun is a red run that passed, and greenRun a green run that passed.
const redRun = {
  id: "r-red",
  phase: "red",
  source: "captured",
  state: "ended",
  verdict: "fails",
  passed: true,
  exit: 1,
  revision,
};
const greenRun = {
  id: "r-green",
  phase: "green",
  source: "captured",
  state: "ended",
  verdict: "holds",
  passed: true,
  exit: 0,
  revision,
};

// build is a live build: task t1 is running its implementing step and names R1, and task t2 is planned and names R2.
// Each test spreads it and names the fields it changes.
const build = {
  epic: liveEpic,
  buildTasks: [
    { id: "t1", title: "Login", status: "dispatched", requirements: ["R1"] },
    { id: "t2", title: "Logout", status: "", requirements: ["R2"] },
  ],
  steps: [
    {
      id: "s1",
      task: "t1",
      agent: "builder",
      stage: "implement",
      started: 9_000n,
      stageStarted: 9_500n,
      doing: "writing the login",
    },
  ],
  requirements: [
    { id: "R1", text: "A user signs in" },
    { id: "R2", text: "A user signs out" },
  ],
};

// detail makes the epic's response from its fields.
function detail(init: MessageInitShape<typeof EpicsGetResponseSchema>): EpicsGetResponse {
  return create(EpicsGetResponseSchema, init);
}

// noArtifact reads no artifact: no test here opens a log.
const noArtifact: LoadArtifact = async () => {
  throw new Error("no artifact is read in this test");
};

// recorder makes commands that record each call as a word in calls.
function recorder(): { calls: string[]; commands: EpicCommands } {
  const calls: string[] = [];
  const commands: EpicCommands = {
    pause: () => {
      calls.push("pause");
    },
    resume: () => {
      calls.push("resume");
    },
    stop: () => {
      calls.push("stop");
    },
    retry: () => {
      calls.push("retry");
    },
    answer: async (questionId, choice, text) => {
      calls.push(`answer ${questionId} ${choice} ${text}`);
      return "";
    },
  };
  return { calls, commands };
}

// withPage renders the epic page for the detail into a fresh test DOM, runs the check on its container, and removes the
// DOM. extra replaces any prop the page takes.
async function withPage(
  epic: EpicsGetResponse,
  check: (container: HTMLElement) => void | Promise<void>,
  extra: Partial<Props> = {},
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  const props: Props = {
    detail: epic,
    history: [],
    now,
    loadArtifact: noArtifact,
    commands: recorder().commands,
    numberKeys: true,
    onOpenContract: () => {},
    onBack: () => {},
    ...extra,
  };
  try {
    await act(() => {
      render(<EpicPage {...props} />, container);
    });
    await check(container);
  } finally {
    await act(() => {
      render(null, container);
    });
    await removeTestDom(window);
  }
}

function textOf(root: ParentNode, selector: string): string | null {
  return root.querySelector(selector)?.textContent ?? null;
}

function textsOf(root: ParentNode, selector: string): string[] {
  return [...root.querySelectorAll(selector)].map((element) => element.textContent ?? "");
}

function has(root: ParentNode, selector: string): boolean {
  return root.querySelector(selector) !== null;
}

// labelsOf is the class of each child of the element the selector finds, or its tag when it has no class.
function labelsOf(root: ParentNode, selector: string): string[] {
  const parent = root.querySelector(selector);
  if (parent === null) {
    return [];
  }
  return [...parent.children].map((child) => child.getAttribute("class") ?? child.tagName.toLowerCase());
}

// press clicks the first element the selector finds, and lets Preact draw what the click changed.
async function press(container: HTMLElement, selector: string): Promise<void> {
  const target = container.querySelector(selector);
  assert.ok(target !== null, `no element matches ${selector}`);
  await act(() => {
    target.dispatchEvent(new Event("click"));
  });
}

// pressButton clicks the button in the scope whose text is the label, and lets Preact draw what the click changed.
async function pressButton(container: HTMLElement, scope: string, label: string): Promise<void> {
  const target = [...container.querySelectorAll<HTMLButtonElement>(`${scope} button`)].find(
    (button) => button.textContent === label,
  );
  assert.ok(target !== undefined, `no ${label} button in ${scope}`);
  await act(() => {
    target.dispatchEvent(new Event("click"));
  });
}

test("the sections come in order: title row, summary, now block, red proof, implementation, green proof, handover, release proof, report", async () => {
  await withPage(
    detail({ ...build, summary: "Ship the demo", contract: { title: "Show the accepted contract" } }),
    (container) => {
      assert.deepEqual(
        labelsOf(container, ".epic-page").map((label) => label.replace(" dim", "")),
        [
          "title-row",
          "summary",
          "now-block",
          "proof red",
          "implementation",
          "proof green",
          "handover",
          "proof release",
          "proof report",
        ],
      );
    },
    { handover: <p>Handed over</p> },
  );
});

test("the implementation section draws the orrery, Now, Next and Just happened, then the workflow only when the epic has nodes", async () => {
  await withPage(detail({ ...build }), (container) => {
    assert.deepEqual(labelsOf(container, ".implementation"), ["orrery", "happen now", "happen next", "happen just"]);
  });
  await withPage(
    detail({ ...build, nodes: [{ path: "build", node: "build", kind: "build", state: "running", round: 1n }] }),
    (container) => {
      assert.deepEqual(labelsOf(container, ".implementation"), [
        "orrery",
        "happen now",
        "happen next",
        "happen just",
        "section",
      ]);
    },
  );
});

test("a running epic shows its state as building, with Pause and Stop", async () => {
  await withPage(detail({ ...build }), (container) => {
    assert.equal(textOf(container, ".title-row .state"), "building");
    assert.deepEqual(textsOf(container, ".title-row .acts > *"), ["Pause", "Stop…"]);
  });
});

test("the title row's state reads its time once the phase has begun, and the state alone at phase zero", async () => {
  await withPage(detail({ ...build, epic: { ...liveEpic, phaseAt: 7_060n } }), (container) => {
    assert.equal(textOf(container, ".title-row .state"), "building · 49 min");
  });
  await withPage(detail({ ...build, epic: { ...liveEpic, phaseAt: 0n } }), (container) => {
    assert.equal(textOf(container, ".title-row .state"), "building");
  });
  // A browser clock behind the server's stamp reads no time below zero.
  await withPage(detail({ ...build, epic: { ...liveEpic, phaseAt: 10_003n } }), (container) => {
    assert.equal(textOf(container, ".title-row .state"), "building · 0 s");
  });
});

test("a paused epic shows its state as paused, with Resume and Stop", async () => {
  await withPage(detail({ ...build, paused: true }), (container) => {
    assert.equal(textOf(container, ".title-row .state"), "paused");
    assert.deepEqual(textsOf(container, ".title-row .acts > *"), ["Resume", "Stop…"]);
  });
});

// orrerydEpic is a live orreryd epic in accepted: its outcome is its phase. Each test names the phase it changes.
const orrerydEpic = { ...liveEpic, source: "orreryd", outcome: "accepted" };

test("LiveControls: an accepted orreryd epic shows Pause and Stop, and reads planning with no task, then building", async () => {
  await withPage(detail({ ...build, buildTasks: [], epic: orrerydEpic }), (container) => {
    assert.equal(textOf(container, ".title-row .state"), "planning");
    assert.deepEqual(textsOf(container, ".title-row .acts > *"), ["Pause", "Stop…"]);
  });
  await withPage(detail({ ...build, epic: orrerydEpic }), (container) => {
    assert.equal(textOf(container, ".title-row .state"), "building");
    assert.deepEqual(textsOf(container, ".title-row .acts > *"), ["Pause", "Stop…"]);
  });
});

test("LiveControls: a paused orreryd epic shows Resume and Stop, and reads paused", async () => {
  await withPage(detail({ ...build, paused: true, epic: { ...orrerydEpic, outcome: "built" } }), (container) => {
    assert.equal(textOf(container, ".title-row .state"), "paused");
    assert.deepEqual(textsOf(container, ".title-row .acts > *"), ["Resume", "Stop…"]);
  });
});

test("LiveControls: a releasing orreryd epic shows Stop and no Pause, and reads releasing", async () => {
  await withPage(detail({ ...build, epic: { ...orrerydEpic, outcome: "releasing" } }), (container) => {
    assert.equal(textOf(container, ".title-row .state"), "releasing");
    assert.deepEqual(textsOf(container, ".title-row .acts > *"), ["Stop…"]);
  });
});

test("LiveControls: a paused releasing orreryd epic shows Resume, and reads paused", async () => {
  await withPage(detail({ ...build, paused: true, epic: { ...orrerydEpic, outcome: "releasing" } }), (container) => {
    assert.equal(textOf(container, ".title-row .state"), "paused");
    assert.deepEqual(textsOf(container, ".title-row .acts > *"), ["Resume", "Stop…"]);
  });
});

test("LiveControls: a released orreryd epic shows no command, and reads released", async () => {
  await withPage(detail({ ...build, epic: { ...orrerydEpic, live: false, outcome: "released" } }), (container) => {
    assert.equal(textOf(container, ".title-row .state"), "released");
    assert.deepEqual(textsOf(container, ".title-row .acts > *"), []);
  });
});

test("LiveControls: a record epic with an empty outcome still shows Pause and Stop", async () => {
  await withPage(detail({ ...build, epic: { ...liveEpic, source: "record" } }), (container) => {
    assert.deepEqual(textsOf(container, ".title-row .acts > *"), ["Pause", "Stop…"]);
  });
});

test("an epic that is not live, or has an outcome, offers no command", async () => {
  for (const epic of [
    { ...liveEpic, live: false, outcome: "released" },
    { ...liveEpic, outcome: "stopped" },
  ]) {
    await withPage(detail({ ...build, epic }), (container) => {
      assert.deepEqual(textsOf(container, ".title-row .acts > *"), []);
    });
  }
});

test("Stop in the title row asks about its slug beside Pause", async () => {
  await withPage(detail({ ...build }), async (container) => {
    await pressButton(container, ".title-row .acts", "Stop…");
    assert.deepEqual(textsOf(container, ".title-row .acts > *"), [
      "Pause",
      "Stop demo-epic?",
      "Stop demo-epic",
      "Keep it running",
    ]);
  });
});

test("the blocker's Stop asks first too, and only its confirm stops the epic", async () => {
  const { calls, commands } = recorder();
  await withPage(
    detail({ ...build, questions: [{ id: "Q1", text: "Which branch?", asked: 9_880n }] }),
    async (container) => {
      await pressButton(container, ".now-block", "Stop…");
      assert.equal(textOf(container, ".now-block .sure"), "Stop demo-epic?");
      assert.deepEqual(calls, []);
      await pressButton(container, ".now-block", "Stop demo-epic");
      assert.deepEqual(calls, ["stop"]);
    },
    { commands },
  );
});

test("the title row's blocker reads its text, and its time once it has begun", async () => {
  await withPage(detail({ ...build, questions: [{ id: "Q1", text: "Which branch?", asked: 9_880n }] }), (container) => {
    assert.equal(textOf(container, ".title-row .blocker"), "waits on your answer · 2 min");
  });
  await withPage(detail({ ...build, parked: "usage limit" }), (container) => {
    assert.equal(textOf(container, ".title-row .blocker"), "parked · usage limit");
  });
  await withPage(detail({ ...build }), (container) => {
    assert.equal(has(container, ".title-row .blocker"), false);
  });
});

test("the summary opens the contract, and reads the contract's title when the epic has no summary", async () => {
  const opened: string[] = [];
  await withPage(
    detail({ ...build, summary: "Ship the demo", contract: { title: "Show the accepted contract" } }),
    async (container) => {
      assert.equal(textOf(container, ".summary"), "Ship the demo");
      await press(container, ".summary");
      assert.deepEqual(opened, ["contract"]);
    },
    {
      onOpenContract: () => {
        opened.push("contract");
      },
    },
  );
  await withPage(detail({ ...build, contract: { title: "Show the accepted contract" } }), (container) => {
    assert.equal(textOf(container, ".summary"), "Show the accepted contract");
  });
});

test("the summary is absent without a contract", async () => {
  await withPage(detail({ ...build, summary: "Ship the demo" }), (container) => {
    assert.equal(has(container, ".summary"), false);
  });
});

test("the epic's problem shows as an error line right before the now block, and only when it is not empty", async () => {
  await withPage(
    detail({ ...build, epic: { ...liveEpic, problem: "state.json: unexpected end of input" } }),
    (container) => {
      assert.equal(textOf(container, "p.problem.error"), "state.json: unexpected end of input");
      const sections = labelsOf(container, ".epic-page");
      assert.equal(sections[sections.indexOf("now-block") - 1], "problem error");
    },
  );
  await withPage(detail({ ...build, epic: { ...liveEpic, problem: "" } }), (container) => {
    assert.equal(has(container, ".problem"), false);
  });
});

test("a running step draws one glow in each stage: turn, checks, gate, judge and a read-only step", async () => {
  const stages = [
    { phase: "turn", stage: { stage: "turn" } },
    { phase: "checks", stage: { stage: "checks" } },
    { phase: "gate", stage: { stage: "gate" } },
    { phase: "judge", stage: { stage: "judge" } },
    { phase: "read-only", stage: { readOnly: true } },
  ];
  for (const { phase, stage } of stages) {
    await withPage(
      detail({
        ...build,
        steps: [
          { id: "s1", task: "t1", agent: "builder", started: 9_000n, stageStarted: 9_500n, doing: "working", ...stage },
        ],
      }),
      (container) => {
        assert.equal(container.querySelectorAll("path.glow").length, 1, phase);
      },
    );
  }
});

test("a red or green proof is dim until its phase is reached", async () => {
  await withPage(detail({ ...build }), (container) => {
    assert.equal(has(container, ".proof.red.dim"), true);
    assert.equal(has(container, ".proof.green.dim"), true);
  });
  await withPage(
    detail({
      ...build,
      requirements: [
        { id: "R1", text: "A user signs in", runs: [redRun] },
        { id: "R2", text: "A user signs out", runs: [greenRun] },
      ],
    }),
    (container) => {
      assert.equal(has(container, ".proof.red.dim"), false);
      assert.equal(has(container, ".proof.green.dim"), false);
    },
  );
});

test("the release proof is dim until the outcome begins with released", async () => {
  await withPage(detail({ ...build }), (container) => {
    assert.equal(has(container, ".proof.release.dim"), true);
  });
  await withPage(detail({ ...build, epic: { ...liveEpic, live: false, outcome: "released to production" } }), (container) => {
    assert.equal(has(container, ".proof.release.dim"), false);
  });
});

test("the release proof is reached by a step that is running or failed, and draws each step's state, evidence and reason", async () => {
  const contract = {
    title: "Ship the demo",
    releaseSteps: [{ step: "tag the release", target: "the git remote", undo: "delete the tag" }],
    releaseThen: "The epic is released.",
  };
  await withPage(detail({ ...build, contract, releaseSteps: [{ position: 1n, state: "running" }] }), (container) => {
    assert.equal(has(container, ".proof.release.dim"), false);
    assert.deepEqual(textsOf(container, ".release-step .state"), ["running"]);
  });
  await withPage(
    detail({
      ...build,
      contract,
      releaseSteps: [{ position: 1n, state: "failed", evidence: "push rejected", reason: "the remote refused the tag" }],
    }),
    (container) => {
      assert.equal(has(container, ".proof.release.dim"), false);
      assert.deepEqual(textsOf(container, ".release-step .state"), ["failed"]);
      assert.deepEqual(textsOf(container, ".release-step .evidence"), ["push rejected"]);
      assert.deepEqual(textsOf(container, ".release-step .reason"), ["the remote refused the tag"]);
    },
  );
  await withPage(detail({ ...build, contract, releaseSteps: [{ position: 1n, state: "waiting" }] }), (container) => {
    assert.equal(has(container, ".proof.release.dim"), true);
  });
});

test("the report is dim until the epic writes one, then it shows the outcome, the ids and why", async () => {
  await withPage(detail({ ...build }), (container) => {
    assert.equal(has(container, ".proof.report.dim"), true);
  });
  await withPage(
    detail({
      ...build,
      report: { outcome: "released to production", shipped: ["R1"], notShipped: ["R2"], why: "R2 failed its green run" },
    }),
    (container) => {
      assert.equal(has(container, ".proof.report.dim"), false);
      assert.equal(textOf(container, ".report .outcome"), "released to production");
      assert.equal(textOf(container, ".report .shipped"), "shipped R1");
      assert.equal(textOf(container, ".report .not-shipped"), "not shipped R2");
      assert.equal(textOf(container, ".report .why"), "R2 failed its green run");
    },
  );
});

test("the actions render first in the title row, before Pause and Stop", async () => {
  await withPage(
    detail({ ...build }),
    (container) => {
      assert.deepEqual(textsOf(container, ".title-row .acts > *"), ["Talk", "Pause", "Stop…"]);
    },
    { actions: <button type="button">Talk</button> },
  );
});

test("onOpenPhase reaches a phase cell, a proof card and a Now and a Next row", async () => {
  const opened: string[] = [];
  await withPage(
    detail({ ...build }),
    async (container) => {
      await press(container, 'path[data-task="t1"][data-phase="checking"]');
      await press(container, ".proof.red article.proof-card.open");
      await press(container, ".happen.now li.open");
      await press(container, ".happen.next li.open");
      assert.deepEqual(opened, ["t1 checking", "t1 implementing", "t1 implementing", "t2 implementing"]);
    },
    {
      onOpenPhase: (task, phase) => {
        opened.push(`${task} ${phase}`);
      },
    },
  );
});

test("the handover renders only when given", async () => {
  await withPage(detail({ ...build }), (container) => {
    assert.equal(has(container, "section.handover"), false);
  });
  await withPage(
    detail({ ...build }),
    (container) => {
      assert.equal(textOf(container, "section.handover"), "Handed over");
    },
    { handover: <p>Handed over</p> },
  );
});
