import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { type EpicCommands, EpicPage, type SessionTarget, type SteerSheet } from "./epicpage";
import type { LoadArtifact } from "./evidence";
import {
  type EpicHistoryEntry,
  EpicHistoryEntrySchema,
  EpicSchema,
  type EpicsGetResponse,
  EpicsGetResponseSchema,
} from "./gen/orrery/v1/epics_pb";
import type { Row } from "./rows";
import { assertSame, installTestDom, removeTestDom } from "./testdom";

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
    release: async (undoNone) => {
      calls.push(`release ${JSON.stringify(undoNone)}`);
      return { kind: "landed" };
    },
    close: async (kind, by) => {
      calls.push(`close ${kind} ${by}`);
      return { kind: "landed" };
    },
    reread: async () => {
      calls.push("reread");
      return true;
    },
  };
  return { calls, commands };
}

// Drawn is what withPage's check gets besides the container: the elements the page brought into view, in order, and
// redraw, which draws the page again with the detail a later read answers.
interface Drawn {
  scrolled: Element[];
  redraw: (next: EpicsGetResponse) => Promise<void>;
}

// withPage renders the epic page for the detail into a fresh test DOM, runs the check on its container and what it
// drew, and removes the DOM. extra replaces any prop the page takes. The window is fresh, so its scrollIntoView can be
// replaced here: happy-dom has no layout, so its own does nothing.
async function withPage(
  epic: EpicsGetResponse,
  check: (container: HTMLElement, drawn: Drawn) => void | Promise<void>,
  extra: Partial<Props> = {},
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  const scrolled: Element[] = [];
  HTMLElement.prototype.scrollIntoView = function scrollIntoView(this: Element) {
    scrolled.push(this);
  };
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
  const redraw = (next: EpicsGetResponse) =>
    act(() => {
      render(<EpicPage {...props} detail={next} />, container);
    });
  try {
    await act(() => {
      render(<EpicPage {...props} />, container);
    });
    await check(container, { scrolled, redraw });
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
        "release",
        "report",
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

test("AskAnswer: the blocker's question is the page's Ask, aged from the page's now, which a digit picks and confirm answers", async () => {
  const { calls, commands } = recorder();
  const question = {
    id: "Q1",
    text: "Which branch?",
    asked: 9_880n,
    choices: [
      { id: "main", does: "Use main." },
      { id: "dev", does: "Use dev." },
    ],
  };
  await withPage(
    detail({ ...build, questions: [question] }),
    async (container) => {
      assert.equal(textOf(container, "section.now .ask .q"), "Which branch?");
      assert.equal(textOf(container, "section.now .ask .age"), "· 2m ago");
      await act(() => {
        document.dispatchEvent(new KeyboardEvent("keydown", { key: "2", cancelable: true }));
      });
      assert.deepEqual(calls, [], "a digit answered the question");
      await pressButton(container, "section.now .ask", "Answer with dev");
      assert.deepEqual(calls, ["answer Q1 dev Use dev."]);
    },
    { commands },
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
      assert.equal(textOf(container, ".tasks .sec .n"), "1 of 2 done");
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
      assert.equal(textOf(container, ".requirements .sec .n"), "1 of 2 proven");
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

test("EpicRecord: a building page's Now draws its step rows, one for each running step", async () => {
  await withPage(detail({ ...build, epic: orrerydEpic }), (container) => {
    assert.deepEqual(textsOf(container, ".now .tbl.steps .tr .doing"), ["writing the login"]);
  });
});

test("EpicRecord: a record epic's page draws its running steps' rows", async () => {
  await withPage(detail({ ...build }), (container) => {
    assert.deepEqual(textsOf(container, ".now .tbl.steps .tr .doing"), ["writing the login"]);
  });
});

test("EpicRecord: Just happened draws the newest three entries, and +n earlier shows the rest", async () => {
  const entries = [1, 2, 3, 4, 5].map((n) =>
    create(EpicHistoryEntrySchema, {
      cursor: `h${n}`,
      time: BigInt(9_000 + n * 100),
      item: { case: "tell", value: { text: `entry ${n}` } },
    }),
  );
  await withPage(
    detail({ ...build, epic: orrerydEpic }),
    async (container) => {
      assert.deepEqual(textsOf(container, ".happen.just .what"), ["entry 5", "entry 4", "entry 3"]);
      assert.deepEqual(textsOf(container, ".happen.just .more"), ["+2 earlier"]);
      await press(container, ".happen.just .more");
      assert.deepEqual(textsOf(container, ".happen.just .what"), ["entry 5", "entry 4", "entry 3", "entry 2", "entry 1"]);
      assert.equal(has(container, ".happen.just .more"), false);
    },
    { history: entries },
  );
});

// steererRow is the listed row of session s2, whose archive id is its own id: the row the Steered from door titles from.
const steererRow: Row = {
  putOff: false,
  key: "session:s2",
  id: "s2",
  archiveId: "s2",
  kind: "session",
  label: "second",
  project: "/home/dev/lighthouse/rota-export",
  host: "",
  state: "",
  summary: "",
  question: "",
  questionHash: "",
  changed: 0,
  seen: 0,
  unseen: false,
  archived: false,
  archiveKind: "session",
  tool: "",
};

test("EpicCommands: the Steered from door draws whenever the epic names its steerer, titled from the listed row, and opens its target", async () => {
  const opened: SessionTarget[] = [];
  await withPage(
    detail({ ...build, epic: { ...orrerydEpic, steering: "s2" }, summary: "Ship the demo" }),
    async (container) => {
      assert.equal(textOf(container, ".summary .door.steered"), "Steered from rota-export second");
      await press(container, ".summary .door.steered");
      assert.deepEqual(opened, [{ kind: "session", id: "s2", label: "second" }]);
    },
    {
      rows: [steererRow],
      onOpenSession: (target) => {
        opened.push(target);
      },
    },
  );
  await withPage(
    detail({ ...build, epic: { ...orrerydEpic, steering: "s9" }, summary: "Ship the demo" }),
    async (container) => {
      assert.equal(textOf(container, ".summary .door.steered"), "Steered from session");
      await press(container, ".summary .door.steered");
      assert.deepEqual(opened.at(-1), { kind: "session", id: "s9", label: "" });
    },
    {
      rows: [steererRow],
      onOpenSession: (target) => {
        opened.push(target);
      },
    },
  );
  await withPage(
    detail({ ...build, epic: { ...orrerydEpic, steering: "" }, summary: "Ship the demo" }),
    (container) => {
      assert.equal(has(container, ".door.steered"), false);
    },
    { rows: [steererRow] },
  );
});

test("EpicCommands: the problem line reads the epic's problem", async () => {
  await withPage(detail({ ...build, epic: { ...orrerydEpic, problem: "state.json: unexpected end of input" } }), (container) => {
    assert.equal(textOf(container, ".problem"), "state.json: unexpected end of input");
  });
});

test("EpicWords: a page whose runs and history carry 40-character revisions shows no revision in its text", async () => {
  await withPage(
    detail({
      ...build,
      epic: orrerydEpic,
      requirements: [
        { id: "R1", text: "A user signs in", runs: [redRun, greenRun] },
        { id: "R2", text: "A user signs out" },
      ],
    }),
    async (container) => {
      await press(container, ".requirements .tbl.reqs .tr");
      assert.equal(container.querySelectorAll(".run-line").length, 2);
      assert.equal(/\b[0-9a-f]{7,}\b/.exec(container.textContent ?? ""), null);
    },
    {
      history: [
        create(EpicHistoryEntrySchema, {
          cursor: "h2",
          time: 9_600n,
          item: { case: "evidence", value: greenRun },
        }),
      ],
    },
  );
});

// row is a listed row with the fields the tests do not set at their defaults.
function row(init: Partial<Row> & Pick<Row, "key">): Row {
  return {
    putOff: false,
    tool: "",
    id: init.key,
    kind: "session",
    label: init.key,
    project: "/work/app",
    host: "box",
    state: "done",
    summary: "",
    question: "",
    questionHash: "",
    changed: 0,
    seen: 0,
    unseen: false,
    archived: false,
    archiveKind: "session",
    archiveId: "",
    ...init,
  };
}

test("MoveLines: the epic page names a session its move line mentions by its listed row's label", async () => {
  const move: EpicHistoryEntry = create(EpicHistoryEntrySchema, {
    time: 400n,
    cursor: "m1",
    item: {
      case: "action",
      value: { seq: 1n, kind: "move", time: 400n, by: "owner", session: "", from: "sess-rota", to: "sess-lamp" },
    },
  });
  const rows = [row({ key: "session:sess-rota", archiveId: "sess-rota", label: "Planned the rota export" })];
  await withPage(
    detail({ ...build, epic: orrerydEpic }),
    (container) => {
      assert.equal(
        textOf(container, "section.happen.just .what"),
        "moved to session sess-lamp from “Planned the rota export”, by you",
      );
    },
    { history: [move], rows },
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

test("EpicAsk: an open question draws its Ask in Now, under the sentence", async () => {
  await withPage(
    detail({ ...build, questions: [{ id: "Q1", text: "Which branch?", asked: 9_880n }] }),
    (container) => {
      assert.deepEqual(labelsOf(container, ".now"), ["sec", "now-sentence", "ask", "tbl steps"]);
      assert.equal(textOf(container, ".now .ask .q"), "Which branch?");
      assert.equal(textOf(container, ".now-sentence"), "One task is being worked on, and one waits on you.");
    },
  );
});

test("EpicAsk: an owner's park shows its Ask with its reason and Retry, and Stop… that asks first and sends one stop on confirm", async () => {
  const { calls, commands } = recorder();
  await withPage(
    detail({ ...build, parked: "usage limit" }),
    async (container) => {
      assert.equal(textOf(container, ".now .ask .head .lbl"), "Asks you");
      assert.equal(textOf(container, ".now .ask .q"), "Usage limit.");
      assert.deepEqual(textsOf(container, ".now .ask .opts > .opt .nm"), ["Retry"]);
      assert.deepEqual(textsOf(container, ".now .doors button"), ["Stop…"]);
      await pressButton(container, ".now .doors", "Stop…");
      assert.equal(textOf(container, ".now .doors .sure"), "Stop demo-epic?");
      assert.deepEqual(calls, []);
      await pressButton(container, ".now .doors", "Stop demo-epic");
      assert.deepEqual(calls, ["stop"]);
    },
    { commands },
  );
});

test("EpicAsk: an owner's park's Retry, picked then confirmed, sends one retry", async () => {
  const { calls, commands } = recorder();
  await withPage(
    detail({ ...build, parked: "usage limit" }),
    async (container) => {
      await press(container, ".now .ask .opts > .opt");
      assert.deepEqual(calls, [], "the pick sends nothing");
      await pressButton(container, ".now .ask", "Answer with Retry");
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
      assert.deepEqual(dialogTitles(container), ["Stop demo-epic?"]);
      assert.equal(textOf(container, '[aria-label="Stop demo-epic?"] p'), "Its running steps are stopped. Nothing is released.");
      assert.deepEqual(textsOf(container, '[aria-label="Stop demo-epic?"] .foot button'), ["Keep it running", "Stop demo-epic"]);
      assert.equal(container.querySelector('[aria-label="Stop demo-epic?"] .foot button.danger')?.textContent ?? null, "Stop demo-epic");
      await pressButton(container, '[aria-label="Stop demo-epic?"]', "Keep it running");
      assert.deepEqual(stopping.calls, []);
      assert.deepEqual(dialogTitles(container), []);
      await pressButton(container, ".cmdbar", "More");
      await pressButton(container, '[aria-label="demo-epic"]', "Stop…");
      await pressButton(container, '[aria-label="Stop demo-epic?"]', "Stop demo-epic");
      assert.deepEqual(stopping.calls, ["stop"]);
      assert.deepEqual(dialogTitles(container), []);
    },
    { narrow: true, commands: stopping.commands },
  );
});

// steerProbe is a steer the tests give the page: it draws a mark of the sheet it is given, open or shut, and keeps the
// last sheet so a test can close it.
function steerProbe(): { steer: NonNullable<Props["steer"]>; last: () => SteerSheet | undefined } {
  let last: SteerSheet | undefined;
  return {
    steer: (sheet) => {
      last = sheet;
      return <span class={sheet.open ? "steer-probe open" : "steer-probe"} />;
    },
    last: () => last,
  };
}

test("EpicCommands: at the desk the steer is drawn in the title row, shut, and the foot is not drawn", async () => {
  const probe = steerProbe();
  await withPage(
    detail({ ...build }),
    (container) => {
      assert.ok(has(container, ".title-row .steer-probe"), "the steer is in the title row");
      assert.equal(has(container, ".steer-probe.open"), false, "its sheet is shut");
      assert.equal(has(container, ".foot-probe"), false, "the foot is not drawn at the desk");
    },
    { steer: probe.steer, foot: <p class="foot-probe" /> },
  );
});

test("EpicCommands: narrow's More lists Steer it from another conversation after the commands, and it opens the steer's sheet in More's place", async () => {
  const probe = steerProbe();
  await withPage(
    detail({ ...build }),
    async (container) => {
      assert.equal(has(container, ".title-row .steer-probe"), false, "the steer is not in the title row");
      assert.ok(has(container, ".steer-probe:not(.open)"), "the steer is drawn shut");
      await pressButton(container, ".cmdbar", "More");
      assert.deepEqual(textsOf(container, '[aria-label="demo-epic"] button'), [
        "Pause",
        "Stop…",
        "Steer it from another conversation",
      ]);
      await pressButton(container, '[aria-label="demo-epic"]', "Steer it from another conversation");
      assert.deepEqual(dialogTitles(container), [], "More's sheet is closed");
      assert.ok(has(container, ".steer-probe.open"), "the steer's sheet is open");
      await act(() => {
        probe.last()?.onClose();
      });
      assert.ok(has(container, ".steer-probe:not(.open)"), "the steer's close shuts its sheet");
    },
    { narrow: true, steer: probe.steer },
  );
});

test("EpicCommands: narrow draws the foot it is given right above the foot bar, and More keeps the commands", async () => {
  await withPage(
    detail({ ...build }),
    async (container) => {
      const children = labelsOf(container, ".epic-page");
      assert.equal(children[children.indexOf("cmdbar") - 1], "foot-probe", "the foot stands right above the foot bar");
      assert.deepEqual(textsOf(container, ".cmdbar > *"), ["More"]);
      await pressButton(container, ".cmdbar", "More");
      assert.deepEqual(textsOf(container, '[aria-label="demo-epic"] button'), ["Pause", "Stop…"]);
    },
    { narrow: true, foot: <p class="foot-probe" /> },
  );
});

test("EpicCommands: an open Stop confirm leaves only its question and two buttons in the title row, with the steer not drawn beside it", async () => {
  const probe = steerProbe();
  await withPage(
    detail({ ...build }),
    async (container) => {
      assert.ok(has(container, ".title-row .steer-probe"), "the steer is in the title row before Stop asks");
      await pressButton(container, ".title-row .acts", "Stop…");
      assert.deepEqual(textsOf(container, ".title-row .acts > *"), [
        "Stop demo-epic?",
        "Stop demo-epic",
        "Keep it running",
      ]);
      assert.equal(has(container, ".title-row .steer-probe"), false, "the steer is not in the title row while Stop asks");
    },
    { steer: probe.steer },
  );
});

test("EpicCommands: an owner-parked epic offers Stop… and no Pause, at the desk and in More", async () => {
  await withPage(detail({ ...build, parked: "usage limit" }), async (container) => {
    assert.deepEqual(textsOf(container, ".title-row .acts > *"), ["Stop…"]);
  });
  await withPage(
    detail({ ...build, parked: "usage limit" }),
    async (container) => {
      await pressButton(container, ".cmdbar", "More");
      assert.deepEqual(textsOf(container, '[aria-label="demo-epic"] button'), ["Stop…"]);
    },
    { narrow: true },
  );
});

test("EpicCommands: a planner-parked epic still offers Pause and Stop…, at the desk and in More", async () => {
  await withPage(detail({ ...build, parked: "stuck release", plannerParked: true }), async (container) => {
    assert.deepEqual(textsOf(container, ".title-row .acts > *"), ["Pause", "Stop…"]);
  });
  await withPage(
    detail({ ...build, parked: "stuck release", plannerParked: true }),
    async (container) => {
      await pressButton(container, ".cmdbar", "More");
      assert.deepEqual(textsOf(container, '[aria-label="demo-epic"] button'), ["Pause", "Stop…"]);
    },
    { narrow: true },
  );
});

test("EpicCommands: narrow draws no host strip in the title row, and wide draws it", async () => {
  await withPage(
    detail({ ...build }),
    (container) => {
      assert.equal(has(container, ".title-row .strip-probe"), true, "wide draws the strip");
    },
    { strip: <span class="strip-probe" /> },
  );
  await withPage(
    detail({ ...build }),
    (container) => {
      assert.equal(has(container, ".title-row .strip-probe"), false, "narrow draws no strip");
    },
    { narrow: true, strip: <span class="strip-probe" /> },
  );
});

// pathOf is the repository path the test epics share: the path replacementsOf reads.
const pathOf = "/work/demo-repo";

// reviewed is a reviewing orreryd epic with one reviewer and one finding, the Review section's rows.
const reviewed = {
  ...build,
  epic: { ...orrerydEpic, outcome: "reviewing" },
  summary: "Ship the demo",
  contract,
  reviews: [{ position: 1n, reviewer: "security", reason: "The token is checked", verdict: "holds" }],
  ledger: [
    { id: "security-1", position: 1n, reviewer: "security", claim: "Token not checked", location: "auth.go:12", result: "" },
  ],
};

test("EpicReview: a reviewing epic draws its Review after the Requirements, with its count words and rows", async () => {
  await withPage(detail(reviewed), (container) => {
    const labels = labelsOf(container, ".epic-page");
    assert.deepEqual(labels.slice(-2), ["requirements", "review"]);
    assert.equal(textOf(container, ".review .sec .n"), "1 reviewer · 1 finding");
    assert.equal(textOf(container, '.review [data-reviewer="security"] .wide'), "The token is checked");
    assert.equal(textOf(container, '.review [data-finding="security-1"] .wide'), "Token not checked · auth.go:12");
  });
});

test("EpicReview: an accepted epic draws no Review, even with reviews on its record", async () => {
  await withPage(detail({ ...reviewed, epic: orrerydEpic }), (container) => {
    assert.equal(has(container, ".review"), false);
  });
});

// releasing is an orreryd epic releasing: its step 1 passed and its step 2 is running, and its contract names a Then.
const releasing = {
  ...build,
  epic: { ...orrerydEpic, outcome: "releasing" },
  summary: "Ship the demo",
  contract: { ...contract, releaseThen: "the tag is pushed" },
  releaseSteps: [
    { position: 1n, step: "Tag the release", target: "origin", undo: "git tag -d", state: "passed" },
    { position: 2n, step: "Push the branch", target: "origin", undo: "none", state: "running" },
  ],
};

test("ReleaseTable: a releasing epic draws its Release section with its words, its steps and its Then, and offers no release", async () => {
  await withPage(detail(releasing), (container) => {
    assert.equal(textOf(container, ".release .sec .name"), "Release");
    assert.equal(textOf(container, ".release .sec .n"), "step 2 of 2");
    assert.deepEqual(textsOf(container, ".release .tr .id"), ["1", "2"]);
    assert.equal(textOf(container, ".release .then"), "Then: the tag is pushed");
    assert.equal(has(container, ".release button"), false, "a running release offers no Release from step");
  });
});

test("ReleaseFrom: a stopped epic's Release from step N, confirmed, sends one release of that step", async () => {
  const { calls, commands } = recorder();
  await withPage(
    detail({
      ...releasing,
      epic: { ...orrerydEpic, outcome: "stopped" },
      releaseSteps: [
        { position: 1n, step: "Tag the release", target: "origin", undo: "git tag -d", state: "passed" },
        { position: 2n, step: "Push the branch", target: "origin", undo: "none", state: "failed", reason: "rejected" },
      ],
    }),
    async (container) => {
      await pressButton(container, ".release", "Release from step 2");
      assert.equal(calls.length, 0, "asking sends nothing");
      await pressButton(container, ".release", "Release from step 2");
      await act(async () => {});
      assert.deepEqual(
        calls.filter((call) => call.startsWith("release")),
        ["release [2]"],
        "the confirm sends one release of step 2",
      );
    },
    { commands },
  );
});

// endedWithGaps is an orreryd epic that ended with gaps on the repository path, beside two other epics: one on the same
// path, which Close as… may name, and one on another path, which it may not.
const endedWithGaps = detail({
  ...build,
  epic: { ...orrerydEpic, outcome: "ended_with_gaps", live: false, path: pathOf },
  report: { outcome: "ended_with_gaps", shipped: ["R1"], notShipped: ["R2"], why: "" },
});
const listedEpics = [
  create(EpicSchema, { id: "e1", repository: "demo-repo", slug: "demo-epic", path: pathOf }),
  create(EpicSchema, { id: "demo-repo/next-epic", repository: "demo-repo", slug: "next-epic", path: pathOf }),
  create(EpicSchema, { id: "other-repo/elsewhere", repository: "other-repo", slug: "elsewhere", path: "/work/other" }),
];

test("EpicClose: an ended-with-gaps epic's Close as… lists the listed epics on its path, and sends one close as chosen", async () => {
  const { calls, commands } = recorder();
  await withPage(
    endedWithGaps,
    async (container) => {
      await pressButton(container, ".report", "Close as…");
      assert.deepEqual(
        textsOf(container, ".report select option"),
        ["pick the epic", "next-epic"],
        "the other epic on the path is listed, the epic itself and the other path's are not",
      );
      const pick = container.querySelector<HTMLSelectElement>(".report select");
      assert.ok(pick !== null);
      pick.value = "demo-repo/next-epic";
      await act(() => {
        pick.dispatchEvent(new Event("change"));
      });
      await pressButton(container, ".report", "Close as superseded");
      await act(async () => {});
      assert.deepEqual(
        calls.filter((call) => call.startsWith("close")),
        ["close superseded demo-repo/next-epic"],
      );
    },
    { commands, listed: listedEpics },
  );
});

// endedWithOpen is an orreryd epic that ended with gaps: R1 is proven by its green run and R2 is open, and each task
// names the requirement it proves.
const endedWithOpen = detail({
  ...build,
  epic: { ...orrerydEpic, outcome: "ended_with_gaps", live: false, path: pathOf },
  report: { outcome: "ended_with_gaps", shipped: ["R1"], notShipped: ["R2"], why: "" },
  buildTasks: [
    { id: "t1", title: "Login", status: "done", requirements: ["R1"] },
    { id: "t2", title: "Logout", status: "done", requirements: ["R2"] },
  ],
  requirements: [
    { id: "R1", text: "A user signs in", runs: [redRun, greenRun] },
    { id: "R2", text: "A user signs out" },
  ],
});

test("EpicReport: an ended-with-gaps epic's task rims read proven exactly when their requirement rows read proven", async () => {
  await withPage(endedWithOpen, (container) => {
    const rims = [...container.querySelectorAll(".tasks path.req")].map((rim) => rim.getAttribute("class"));
    const words = [...container.querySelectorAll(".requirements .tbl.reqs .tr .end")].map((end) => end.textContent);
    assert.deepEqual(words, ["proven", "open"]);
    assert.deepEqual(rims, ["req proven", "req"], "each rim follows the row of the requirement its task names");
  });
});

// endedReleased is an orreryd epic that released, with its Report drawn.
const endedReleased = detail({
  ...build,
  epic: { ...orrerydEpic, outcome: "released", live: false, path: pathOf },
  report: { outcome: "released", shipped: ["R1"], notShipped: [], why: "" },
});

test("EpicReport: an ended epic opens scrolled to its Report, once", async () => {
  await withPage(endedReleased, (container, drawn) => {
    assert.equal(drawn.scrolled.length, 1, "the page brings one block into view");
    assertSame(drawn.scrolled[0], container.querySelector('section[aria-label="Report"]'));
  });
});

test("EpicReport: a landing that names no block still brings the Report into view", async () => {
  await withPage(
    endedReleased,
    (container, drawn) => {
      assert.equal(drawn.scrolled.length, 1);
      assertSame(drawn.scrolled[0], container.querySelector('section[aria-label="Report"]'));
    },
    { landing: { kind: "" } },
  );
});

test("EpicReport: a question landing still lands on Now, and does not bring the Report into view", async () => {
  await withPage(
    detail({
      ...build,
      epic: { ...orrerydEpic, outcome: "released", live: false, path: pathOf },
      report: { outcome: "released", shipped: ["R1"], notShipped: [], why: "" },
      questions: [{ id: "Q1", text: "Which branch?", asked: 9_880n }],
    }),
    (container, drawn) => {
      assert.equal(drawn.scrolled.length, 1);
      assertSame(drawn.scrolled[0], container.querySelector("section.now"));
    },
    { landing: { kind: "question" } },
  );
});

test("EpicReport: a later read draws the page again and brings nothing into view again", async () => {
  await withPage(endedReleased, async (container, drawn) => {
    await drawn.redraw(
      detail({
        ...build,
        epic: { ...orrerydEpic, outcome: "released", live: false, path: pathOf },
        report: { outcome: "released", shipped: ["R1"], notShipped: [], why: "Read again" },
      }),
    );
    assert.equal(textOf(container, ".report-why"), "Read again", "the later read is drawn");
    assert.equal(drawn.scrolled.length, 1, "the later read scrolls nothing");
  });
});
