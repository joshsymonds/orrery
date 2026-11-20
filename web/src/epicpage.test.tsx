import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { type EpicCommands, EpicPage } from "./epicpage";
import type { LoadArtifact } from "./evidence";
import { type EpicsGetResponse, EpicsGetResponseSchema, EpicHistoryEntrySchema } from "./gen/orrery/v1/epics_pb";
import { installTestDom, removeTestDom } from "./testdom";
import type { TalkTarget } from "./talk";

type Props = Parameters<typeof EpicPage>[0];

// now is the clock the page measures its times against, in unix seconds.
const now = 10_000;

// liveEpic is a live epic with no outcome, named demo-epic. It is a record epic.
const liveEpic = { id: "e1", slug: "demo-epic", repository: "demo-repo", outcome: "", live: true };

// orrerydEpic is a live orreryd epic in accepted: its outcome is its phase. Each test names the phase it changes.
const orrerydEpic = { ...liveEpic, source: "orreryd", outcome: "accepted" };

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

// contract is an orreryd epic's contract, accepted at zero so its words carry no time.
const contract = { title: "Show the accepted contract", care: "high", accepted: 0n };

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

// history is one entry of the epic's history, which the page draws under Just happened.
const history = [
  create(EpicHistoryEntrySchema, {
    cursor: "h1",
    time: 9_500n,
    item: { case: "trigger", value: { cause: "accepted" } },
  }),
];

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
    onOpenSession: () => {},
    onBack: () => {},
    commandError: "",
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

test("EpicRecord: an orreryd epic draws its sections in order, and none it has not reached", async () => {
  await withPage(
    detail({
      ...build,
      epic: { ...orrerydEpic, problem: "state.json: unexpected end of input" },
      summary: "Ship the demo",
      contract,
      nodes: [{ path: "build", node: "build", kind: "build", state: "running", round: 1n }],
      releaseSteps: [{ position: 1n, state: "running" }],
      report: { outcome: "released", shipped: ["R1"], notShipped: [], why: "" },
    }),
    (container) => {
      assert.deepEqual(labelsOf(container, ".epic-page"), [
        "title-row",
        "problem error",
        "summary",
        "track",
        "now",
        "happen just",
        "tasks",
        "requirements",
      ]);
    },
    { history },
  );
});

test("EpicRecord: a planning epic draws its summary, track, Now and requirements, and no tasks or history", async () => {
  await withPage(
    detail({ ...build, buildTasks: [], epic: orrerydEpic, summary: "Ship the demo", contract }),
    (container) => {
      assert.deepEqual(labelsOf(container, ".epic-page"), ["title-row", "summary", "track", "now", "requirements"]);
    },
  );
});

test("EpicRecord: a record epic draws only its Now and its Tasks", async () => {
  await withPage(
    detail({
      ...build,
      summary: "Ship the demo",
      contract,
      requirements: [{ id: "R1", text: "A user signs in", runs: [greenRun] }],
      report: { outcome: "released", shipped: ["R1"], notShipped: [], why: "" },
    }),
    (container) => {
      assert.deepEqual(labelsOf(container, ".epic-page"), ["title-row", "now", "tasks"]);
    },
    { history },
  );
});

test("EpicRecord: no problem line draws when the epic's problem is empty", async () => {
  await withPage(detail({ ...build, epic: { ...orrerydEpic, problem: "" } }), (container) => {
    assert.equal(has(container, ".problem"), false);
  });
});

test("EpicRecord: the Tasks head counts the integrated tasks, and the chart and the table draw each task", async () => {
  await withPage(
    detail({
      ...build,
      buildTasks: [
        { id: "t1", title: "Login", status: "done", requirements: ["R1"] },
        { id: "t2", title: "Logout", status: "", requirements: ["R2"] },
      ],
    }),
    (container) => {
      assert.equal(textOf(container, ".tasks .sec-head span"), "1 of 2 done");
      assert.equal(container.querySelectorAll(".tasks path.cell").length, 10);
      assert.deepEqual(textsOf(container, ".tasks .tr .strong"), ["Login", "Logout"]);
    },
  );
});

test("EpicRecord: a chart cell and a task row each open their task's phase", async () => {
  const opened: string[] = [];
  await withPage(
    detail({ ...build }),
    async (container) => {
      await press(container, 'path[data-task="t1"][data-place="implement"]');
      await press(container, ".tasks button.tr");
      assert.deepEqual(opened, ["t1 implementing", "t1 implementing"]);
    },
    {
      onOpenPhase: (task, phase) => {
        opened.push(`${task} ${phase}`);
      },
    },
  );
});

test("EpicRecord: the Requirements head counts the proven requirements, and the table draws each row", async () => {
  await withPage(
    detail({
      ...build,
      epic: orrerydEpic,
      requirements: [
        { id: "R1", text: "A user signs in", runs: [redRun, greenRun] },
        { id: "R2", text: "A user signs out" },
      ],
    }),
    (container) => {
      assert.equal(textOf(container, ".requirements .sec-head span"), "1 of 2 proven");
      assert.deepEqual(textsOf(container, ".requirements .tbl.reqs .tr .id"), ["R1", "R2"]);
    },
  );
});

test("EpicRecord: the summary draws the summary paragraph, and the contract door opens the contract", async () => {
  const opened: string[] = [];
  await withPage(
    detail({ ...build, epic: orrerydEpic, summary: "Ship the demo", contract }),
    async (container) => {
      assert.equal(textOf(container, ".summary .summary-text"), "Ship the demo");
      assert.equal(textOf(container, ".summary .door.contract"), "Contract accepted · high care · 2 requirements");
      await press(container, ".summary .door.contract");
      assert.deepEqual(opened, ["contract"]);
    },
    {
      onOpenContract: () => {
        opened.push("contract");
      },
    },
  );
});

test("EpicRecord: the summary reads the contract's title when the epic has no summary, and has no contract door without a contract", async () => {
  await withPage(detail({ ...build, epic: orrerydEpic, contract }), (container) => {
    assert.equal(textOf(container, ".summary .summary-text"), "Show the accepted contract");
  });
  await withPage(detail({ ...build, epic: orrerydEpic, summary: "Ship the demo" }), (container) => {
    assert.equal(textOf(container, ".summary .summary-text"), "Ship the demo");
    assert.equal(has(container, ".door.contract"), false);
  });
});

test("EpicRecord: the Steered from door names the steering session and opens its target, and is absent without one", async () => {
  const opened: TalkTarget[] = [];
  const sessions = [{ id: "s2", pane: "%7", label: "second", call: "epic_get", at: 9_500n, creator: false }];
  await withPage(
    detail({ ...build, epic: { ...orrerydEpic, steering: "s2" }, summary: "Ship the demo", sessions }),
    async (container) => {
      assert.equal(textOf(container, ".summary .door.steered"), "Steered from second");
      await press(container, ".summary .door.steered");
      assert.deepEqual(opened, [{ kind: "pane", id: "%7", label: "second" }]);
    },
    {
      onOpenSession: (target) => {
        opened.push(target);
      },
    },
  );
  await withPage(
    detail({ ...build, epic: { ...orrerydEpic, steering: "" }, summary: "Ship the demo", sessions }),
    (container) => {
      assert.equal(has(container, ".door.steered"), false);
    },
  );
  await withPage(
    detail({ ...build, epic: { ...orrerydEpic, steering: "s9" }, summary: "Ship the demo", sessions }),
    (container) => {
      assert.equal(has(container, ".door.steered"), false);
    },
  );
});

test("EpicRecord: a running step's task draws one running cell in each stage: turn, checks, gate, judge and a read-only step", async () => {
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
        assert.equal(container.querySelectorAll("path.cell.c-run").length, 1, phase);
      },
    );
  }
});

test("EpicAsk: an open question draws its answer card in Now, under the sentence", async () => {
  await withPage(
    detail({ ...build, questions: [{ id: "Q1", text: "Which branch?", asked: 9_880n }] }),
    (container) => {
      assert.deepEqual(labelsOf(container, ".now"), ["h3", "now-sentence", "answer", "tbl steps"]);
      assert.equal(textOf(container, ".now .answer .question"), "Which branch?");
      assert.equal(textOf(container, ".now-sentence"), "One task is being worked on, and one waits on you.");
    },
  );
});

test("EpicAsk: an owner's park shows its reason, Retry, and Stop… that asks first and sends one stop on confirm", async () => {
  const { calls, commands } = recorder();
  await withPage(
    detail({ ...build, parked: "usage limit" }),
    async (container) => {
      assert.equal(textOf(container, ".ask.epic.park .ask-head"), "It asks you what to do");
      assert.equal(textOf(container, ".ask.epic.park .reason"), "Usage limit.");
      assert.deepEqual(textsOf(container, ".ask.epic.park button"), ["Retry", "Stop…"]);
      await pressButton(container, ".ask.epic.park", "Stop…");
      assert.equal(textOf(container, ".ask.epic.park .sure"), "Stop demo-epic?");
      assert.deepEqual(calls, []);
      await pressButton(container, ".ask.epic.park", "Stop demo-epic");
      assert.deepEqual(calls, ["stop"]);
    },
    { commands },
  );
});

test("EpicAsk: an owner's park's Retry sends one retry", async () => {
  const { calls, commands } = recorder();
  await withPage(
    detail({ ...build, parked: "usage limit" }),
    async (container) => {
      await pressButton(container, ".ask.epic.park", "Retry");
      assert.deepEqual(calls, ["retry"]);
    },
    { commands },
  );
});

test("EpicAsk: a planner's park is one quiet line, with no Ask and no button", async () => {
  await withPage(detail({ ...build, parked: "stuck release", plannerParked: true }), (container) => {
    assert.equal(
      textOf(container, ".now p.inote.quiet"),
      "The planner holds this park: stuck release. It will retry, replan, or ask you.",
    );
    assert.equal(has(container, ".ask"), false);
    assert.deepEqual(textsOf(container, ".now button"), []);
  });
});

test("EpicAsk: an epic whose every running step is limited offers Resume now, which sends one resume", async () => {
  const { calls, commands } = recorder();
  await withPage(
    detail({ ...build, steps: [{ ...build.steps[0], limited: true }] }),
    async (container) => {
      assert.equal(textOf(container, ".now-sentence"), "1 step waits on a usage limit. It resumes on its own.");
      await pressButton(container, ".now", "Resume now");
      assert.deepEqual(calls, ["resume"]);
    },
    { commands },
  );
});

test("EpicWords: the state pill reads its word and tone: building is live, paused is grey, and an owner's park is attention", async () => {
  await withPage(detail({ ...build, epic: orrerydEpic }), (container) => {
    assert.equal(textOf(container, ".title-row .state"), "building");
    assert.equal(has(container, ".title-row .state.t-live"), true);
  });
  await withPage(detail({ ...build, epic: orrerydEpic, paused: true }), (container) => {
    assert.equal(textOf(container, ".title-row .state"), "paused");
    assert.equal(has(container, ".title-row .state.t-quiet"), true);
  });
  await withPage(detail({ ...build, epic: orrerydEpic, parked: "usage limit" }), (container) => {
    assert.equal(textOf(container, ".title-row .state"), "parked");
    assert.equal(has(container, ".title-row .state.t-attention"), true);
  });
});

test("EpicWords: the blocker pill names an open question with its time, an owner's park, and nothing for a limit or a planner's park", async () => {
  await withPage(detail({ ...build, questions: [{ id: "Q1", text: "Which branch?", asked: 9_880n }] }), (container) => {
    assert.equal(textOf(container, ".title-row .blocker"), "asks you a question · 2 min");
    assert.equal(has(container, ".title-row .blocker.t-attention"), true);
  });
  await withPage(detail({ ...build, parked: "usage limit" }), (container) => {
    assert.equal(textOf(container, ".title-row .blocker"), "asks you what to do");
  });
  await withPage(
    detail({ ...build, steps: [{ ...build.steps[0], limited: true }] }),
    (container) => {
      assert.equal(has(container, ".title-row .blocker"), false);
    },
  );
  await withPage(detail({ ...build, parked: "stuck release", plannerParked: true }), (container) => {
    assert.equal(has(container, ".title-row .blocker"), false);
  });
});

test("EpicWords: the track draws each stage's words, and the stage now is marked with its tone", async () => {
  await withPage(detail({ ...build, epic: orrerydEpic, summary: "Ship the demo", contract }), (container) => {
    assert.deepEqual(textsOf(container, ".track .ds"), [
      "accepted",
      "0 of 2 tasks done",
      "not reached",
      "not reached",
      "not reached",
    ]);
    assert.equal(has(container, ".track .stage.now.t-live"), true);
  });
});

test("EpicWords: an ended orreryd epic reads its state word in the title row and as its Now sentence", async () => {
  await withPage(detail({ ...build, epic: { ...orrerydEpic, outcome: "released", live: false } }), (container) => {
    assert.equal(textOf(container, ".title-row .state"), "released");
    assert.equal(has(container, ".title-row .state.t-ok"), true);
    assert.equal(textOf(container, ".now-sentence"), "It ended: released.");
  });
});

test("EpicCommands: Pause and Resume each send one call", async () => {
  const pausing = recorder();
  await withPage(
    detail({ ...build, epic: orrerydEpic }),
    async (container) => {
      await pressButton(container, ".title-row .acts", "Pause");
      assert.deepEqual(pausing.calls, ["pause"]);
    },
    { commands: pausing.commands },
  );
  const resuming = recorder();
  await withPage(
    detail({ ...build, epic: orrerydEpic, paused: true }),
    async (container) => {
      await pressButton(container, ".title-row .acts", "Resume");
      assert.deepEqual(resuming.calls, ["resume"]);
    },
    { commands: resuming.commands },
  );
});

test("EpicCommands: Stop… in the title row asks about its slug, and only its confirm sends one stop", async () => {
  const { calls, commands } = recorder();
  await withPage(
    detail({ ...build }),
    async (container) => {
      await pressButton(container, ".title-row .acts", "Stop…");
      assert.deepEqual(textsOf(container, ".title-row .acts > *"), [
        "Pause",
        "Stop demo-epic?",
        "Stop demo-epic",
        "Keep it running",
      ]);
      assert.deepEqual(calls, []);
      await pressButton(container, ".title-row .acts", "Stop demo-epic");
      assert.deepEqual(calls, ["stop"]);
    },
    { commands },
  );
});

test("EpicCommands: a did-not-land line draws under the title row, and no line draws without one", async () => {
  await withPage(detail({ ...build }), (container) => {
    assert.equal(textOf(container, ".title-row + .page-note"), "Pause did not land: the server refused.");
  }, { commandError: "Pause did not land: the server refused." });
  await withPage(detail({ ...build }), (container) => {
    assert.equal(has(container, ".page-note"), false);
  });
});

test("EpicCommands: a running epic shows its state as building, with Pause and Stop", async () => {
  await withPage(detail({ ...build }), (container) => {
    assert.equal(textOf(container, ".title-row .state"), "building");
    assert.deepEqual(textsOf(container, ".title-row .acts > *"), ["Pause", "Stop…"]);
  });
});

test("EpicCommands: the title row's state reads its time once the phase has begun, and the state alone at phase zero", async () => {
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

test("EpicCommands: a paused epic shows its state as paused, with Resume and Stop", async () => {
  await withPage(detail({ ...build, paused: true }), (container) => {
    assert.equal(textOf(container, ".title-row .state"), "paused");
    assert.deepEqual(textsOf(container, ".title-row .acts > *"), ["Resume", "Stop…"]);
  });
});

test("EpicCommands: an accepted orreryd epic shows Pause and Stop, and reads planning with no task, then building", async () => {
  await withPage(detail({ ...build, buildTasks: [], epic: orrerydEpic }), (container) => {
    assert.equal(textOf(container, ".title-row .state"), "planning");
    assert.deepEqual(textsOf(container, ".title-row .acts > *"), ["Pause", "Stop…"]);
  });
  await withPage(detail({ ...build, epic: orrerydEpic }), (container) => {
    assert.equal(textOf(container, ".title-row .state"), "building");
    assert.deepEqual(textsOf(container, ".title-row .acts > *"), ["Pause", "Stop…"]);
  });
});

test("EpicCommands: an epic that is not live, or has an outcome, offers no command", async () => {
  for (const epic of [
    { ...liveEpic, live: false, outcome: "released" },
    { ...liveEpic, outcome: "stopped" },
  ]) {
    await withPage(detail({ ...build, epic }), (container) => {
      assert.deepEqual(textsOf(container, ".title-row .acts > *"), []);
    });
  }
});

test("EpicCommands: the title row's actions render first, before Pause and Stop", async () => {
  await withPage(
    detail({ ...build }),
    (container) => {
      assert.deepEqual(textsOf(container, ".title-row .acts > *"), ["Talk", "Pause", "Stop…"]);
    },
    { actions: <button type="button">Talk</button> },
  );
});

// dialogTitles lists the titles of the sheets drawn, in the order they stack.
function dialogTitles(root: ParentNode): string[] {
  return [...root.querySelectorAll('section[role="dialog"]')].map((dialog) => dialog.getAttribute("aria-label") ?? "");
}

test("EpicCommands: narrow draws no commands in the title row, and its foot holds More alone without a steerer", async () => {
  await withPage(
    detail({ ...build }),
    (container) => {
      assert.deepEqual(textsOf(container, ".title-row .acts > *"), []);
      assert.deepEqual(textsOf(container, ".cmdbar > *"), ["More"]);
    },
    { narrow: true },
  );
});

test("EpicCommands: narrow's foot holds Talk to the session that steers it when a steerer is known, and Talk calls onTalk", async () => {
  let talks = 0;
  await withPage(
    detail({ ...build }),
    async (container) => {
      assert.deepEqual(textsOf(container, ".cmdbar > *"), ["Talk to the session that steers it", "More"]);
      await pressButton(container, ".cmdbar", "Talk to the session that steers it");
      assert.equal(talks, 1, "Talk calls onTalk once");
    },
    {
      narrow: true,
      onTalk: () => {
        talks += 1;
      },
    },
  );
});

test("EpicCommands: More opens a sheet titled with the slug, listing exactly the commands the epic allows", async () => {
  await withPage(
    detail({ ...build }),
    async (container) => {
      await pressButton(container, ".cmdbar", "More");
      assert.deepEqual(dialogTitles(container), ["demo-epic"]);
      assert.deepEqual(textsOf(container, '[aria-label="demo-epic"] button'), ["Pause", "Stop…"]);
    },
    { narrow: true },
  );
  await withPage(
    detail({ ...build, paused: true }),
    async (container) => {
      await pressButton(container, ".cmdbar", "More");
      assert.deepEqual(textsOf(container, '[aria-label="demo-epic"] button'), ["Resume", "Stop…"]);
    },
    { narrow: true },
  );
  await withPage(
    detail({ ...build, epic: { ...liveEpic, live: false, outcome: "released" } }),
    async (container) => {
      await pressButton(container, ".cmdbar", "More");
      assert.deepEqual(textsOf(container, '[aria-label="demo-epic"] button'), []);
    },
    { narrow: true },
  );
});

test("EpicCommands: Pause and Resume in More each send one call, and close the sheet", async () => {
  const pausing = recorder();
  await withPage(
    detail({ ...build }),
    async (container) => {
      await pressButton(container, ".cmdbar", "More");
      await pressButton(container, '[aria-label="demo-epic"]', "Pause");
      assert.deepEqual(pausing.calls, ["pause"]);
      assert.deepEqual(dialogTitles(container), []);
    },
    { narrow: true, commands: pausing.commands },
  );
  const resuming = recorder();
  await withPage(
    detail({ ...build, paused: true }),
    async (container) => {
      await pressButton(container, ".cmdbar", "More");
      await pressButton(container, '[aria-label="demo-epic"]', "Resume");
      assert.deepEqual(resuming.calls, ["resume"]);
      assert.deepEqual(dialogTitles(container), []);
    },
    { narrow: true, commands: resuming.commands },
  );
});

test("EpicCommands: Stop… in More opens the confirm with its words, Keep it running sends nothing, and the confirm sends one stop", async () => {
  const stopping = recorder();
  await withPage(
    detail({ ...build }),
    async (container) => {
      await pressButton(container, ".cmdbar", "More");
      await pressButton(container, '[aria-label="demo-epic"]', "Stop…");
      assert.deepEqual(dialogTitles(container), ["demo-epic", "Stop demo-epic?"]);
      assert.equal(textOf(container, '[aria-label="Stop demo-epic?"] p'), "Its running steps are stopped. Nothing is released.");
      assert.deepEqual(textsOf(container, '[aria-label="Stop demo-epic?"] button'), ["Stop demo-epic", "Keep it running"]);
      await pressButton(container, '[aria-label="Stop demo-epic?"]', "Keep it running");
      assert.deepEqual(stopping.calls, []);
      assert.deepEqual(dialogTitles(container), ["demo-epic"]);
      await pressButton(container, '[aria-label="demo-epic"]', "Stop…");
      await pressButton(container, '[aria-label="Stop demo-epic?"]', "Stop demo-epic");
      assert.deepEqual(stopping.calls, ["stop"]);
      assert.deepEqual(dialogTitles(container), []);
    },
    { narrow: true, commands: stopping.commands },
  );
});
