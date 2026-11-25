import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { EpicPage } from "./epicpage";
import { EpicUsageSection } from "./epicusage";
import type { LoadArtifact } from "./evidence";
import {
  type EpicRequirement,
  EpicRequirementSchema,
  type EpicSession,
  EpicSessionSchema,
  type EpicStep,
  EpicStepSchema,
  type EpicsGetResponse,
  EpicsGetResponseSchema,
  EvidenceRunSchema,
} from "./gen/orrery/v1/epics_pb";
import {
  type UsageEpicSession,
  UsageEpicSessionSchema,
  type UsageEpicTotalsResponse,
  UsageEpicTotalsResponseSchema,
  type UsageSessionTotal,
  UsageSessionTotalSchema,
} from "./gen/orrery/v1/usage_pb";
import { epicRequirements, epicSessions, epicSteps, epicUsage } from "./fixtures/usage";
import { assertNone, installTestDom, removeTestDom } from "./testdom";
import { turn } from "./testwait";

// The section is drawn from invented steps and sessions, and from the epic fixture in ./fixtures/usage. Each subtest reads
// what the DOM shows, row by row, in order. The page's reads are answered by a fake loader, and each wait is one turn of
// the event loop, never a clock.

type Props = Parameters<typeof EpicPage>[0];

// now is the clock the page measures its times against, in unix seconds.
const now = 10_000;

// SectionInit is what drawSection draws: the response's sessions, the epic's steps, requirements and sessions, and whether
// the section is narrow.
interface SectionInit {
  sessions: UsageEpicSession[];
  steps?: EpicStep[];
  requirements?: EpicRequirement[];
  epicSessions?: EpicSession[];
  narrow?: boolean;
}

// totalOf is one total of a session with an empty agent type: its model, its list cost in nano US dollars, and its requests
// and cache writes. It bills nothing.
function totalOf(model: string, list: bigint, requests = 1n, cacheWriteTokens = 0n): UsageSessionTotal {
  return create(UsageSessionTotalSchema, { model, requests, cacheWriteTokens, listCostNanoUsd: list });
}

// sessionOf is a session of the epic with its totals, and whether it is shared.
function sessionOf(session: string, totals: UsageSessionTotal[], shared = false): UsageEpicSession {
  return create(UsageEpicSessionSchema, { session, shared, totals });
}

// stepOf is a step of the epic, whose id is its session's id, with its task, its agent and its role and read-only flag. A
// step has not ended unless ended is given.
function stepOf(
  id: string,
  task: string,
  agent: string,
  init: { role?: string; readOnly?: boolean; ended?: bigint } = {},
): EpicStep {
  return create(EpicStepSchema, {
    id,
    task,
    agent,
    role: init.role ?? "",
    readOnly: init.readOnly ?? false,
    ended: init.ended ?? 0n,
  });
}

// requirementOf is a requirement whose runs are the run ids given.
function requirementOf(id: string, runs: string[]): EpicRequirement {
  return create(EpicRequirementSchema, { id, runs: runs.map((run) => create(EvidenceRunSchema, { id: run })) });
}

// answerOf is the response a usage read answers with.
function answerOf(sessions: UsageEpicSession[]): UsageEpicTotalsResponse {
  return create(UsageEpicTotalsResponseSchema, { sessions });
}

// drawSection draws the section for init into a fresh test DOM, runs check on its container, and removes the DOM.
async function drawSection(init: SectionInit, check: (container: HTMLElement) => void): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(
      <EpicUsageSection
        sessions={init.sessions}
        steps={init.steps ?? []}
        epicSessions={init.epicSessions ?? []}
        requirements={init.requirements ?? []}
        narrow={init.narrow ?? false}
      />,
      container,
    );
    check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

// textOf is the text of the first element that matches selector, and fails the test when none does.
function textOf(root: ParentNode, selector: string): string {
  const element = root.querySelector(selector);
  assert.ok(element !== null, `no element matches ${selector}`);
  return (element.textContent ?? "").trim();
}

// rowsOf reads each session row of the section: its name, model, requests, cache write tokens and list cost, in order.
function rowsOf(container: ParentNode) {
  return [...container.querySelectorAll(".epic-usage-row")].map((row) => ({
    name: textOf(row, ".epic-usage-who"),
    model: textOf(row, ".epic-usage-model"),
    requests: textOf(row, ".epic-usage-requests"),
    cache: textOf(row, ".epic-usage-cache"),
    list: textOf(row, ".epic-usage-list"),
  }));
}

// namesOf is the name of each session row, in order.
function namesOf(container: ParentNode): string[] {
  return [...container.querySelectorAll(".epic-usage-row")].map((row) => textOf(row, ".epic-usage-who"));
}

// markOf is the mark a row carries, or null when it carries none.
function markOf(row: Element): string | null {
  return row.querySelector(".epic-usage-shared")?.textContent ?? null;
}

// widthsOf is the percentage width each element matching selector is drawn at, in document order.
function widthsOf(root: ParentNode, selector: string): number[] {
  return [...root.querySelectorAll(selector)].map((element) => {
    const match = /width:\s*([\d.]+)%/.exec(element.getAttribute("style") ?? "");
    assert.ok(match !== null, `${selector} draws no width`);
    return Number(match[1]);
  });
}

// assertWidths checks each drawn width is within tolerance of the percentage it should be, in order.
function assertWidths(actual: number[], expected: number[], tolerance: number): void {
  assert.equal(actual.length, expected.length, `drew ${actual.length} widths, expected ${expected.length}`);
  expected.forEach((want, i) => {
    assert.ok(
      Math.abs(actual[i] - want) <= tolerance,
      `width ${i} is ${actual[i]}%, expected ${want}% within ${tolerance}`,
    );
  });
}

// fixtureInit is the epic fixture as the page holds it: its usage, steps, requirements and sessions.
function fixtureInit(narrow: boolean): SectionInit {
  return {
    sessions: epicUsage,
    steps: epicSteps,
    requirements: epicRequirements,
    epicSessions,
    narrow,
  };
}

// liveEpic is a live epic named demo-epic, the epic the page draws.
const liveEpic = { id: "e1", slug: "demo-epic", repository: "demo-repo", outcome: "accepted", live: true };

// epicDetail is the epic's response for the steps the page holds.
function epicDetail(steps: EpicStep[]): EpicsGetResponse {
  return create(EpicsGetResponseSchema, { epic: liveEpic, steps });
}

// noArtifact reads no artifact: no test here opens a log.
const noArtifact: LoadArtifact = async () => {
  throw new Error("no artifact is read in this test");
};

// commands are the epic's commands, none of which these tests send.
const commands: Props["commands"] = {
  pause: () => {},
  resume: () => {},
  stop: () => {},
  retry: () => {},
  answer: async () => "",
  release: async () => ({ kind: "landed" }),
  close: async () => ({ kind: "landed" }),
  reread: async () => true,
};

// settle lets the answers of the reads the page made run, one turn of the event loop at a time.
const settle = () => act(turn);

// fakeLoader answers each read in turn with the next answer: a response, or an error that the read fails with. It records
// the epic each read names, and a read past the answers given fails.
function fakeLoader(answers: (UsageEpicTotalsResponse | Error)[]): {
  calls: string[];
  loadUsage: (epic: string) => Promise<UsageEpicTotalsResponse>;
} {
  const calls: string[] = [];
  return {
    calls,
    loadUsage: (epic) => {
      calls.push(epic);
      const answer = answers[calls.length - 1];
      if (answer === undefined) {
        return Promise.reject(new Error("the usage was read more often than the test expected"));
      }
      return answer instanceof Error ? Promise.reject(answer) : Promise.resolve(answer);
    },
  };
}

type Redraw = (next: EpicsGetResponse) => Promise<void>;

// withEpicPage renders the epic page for the detail into a fresh test DOM, with loadUsage as its usage loader, runs the check
// on its container and redraw, which draws the page again with a later detail, and removes the DOM.
async function withEpicPage(
  detail: EpicsGetResponse,
  loadUsage: ((epic: string) => Promise<UsageEpicTotalsResponse>) | undefined,
  check: (container: HTMLElement, redraw: Redraw) => Promise<void>,
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  const props: Props = {
    detail,
    history: [],
    now,
    loadArtifact: noArtifact,
    commands,
    numberKeys: false,
    commandError: "",
    onOpenContract: () => {},
    onOpenSession: () => {},
    loadUsage,
  };
  const redraw: Redraw = async (next) => {
    await act(() => {
      render(<EpicPage {...props} detail={next} />, container);
    });
    await settle();
  };
  try {
    await redraw(detail);
    await check(container, redraw);
  } finally {
    await act(() => {
      render(null, container);
    });
    await removeTestDom(window);
  }
}

test("EpicUsageSection", async (t) => {
  await t.test("draws nothing when the response has no sessions", () =>
    drawSection({ sessions: [] }, (container) => {
      assertNone(container.querySelector(".epic-usage"), "an empty response drew a section");
    }));

  await t.test("sessions read largest list first, ties by session id", () =>
    drawSection(
      {
        sessions: [
          sessionOf("s-b", [totalOf("haiku-5-5", 1_000_000_000n)]),
          sessionOf("s-c", [totalOf("opus-5-5", 2_000_000_000n)]),
          sessionOf("s-a", [totalOf("sonnet-5-5", 1_000_000_000n)]),
        ],
      },
      (container) => {
        assert.deepEqual(namesOf(container), ["s-c", "s-a", "s-b"]);
      },
    ));

  await t.test("the steerer reads steering and is marked shared, and a step it holds still reads steering", () =>
    drawSection(
      {
        sessions: [
          sessionOf("s-w", [totalOf("sonnet-5-5", 1_000_000_000n)]),
          sessionOf("s-st", [totalOf("opus-5-5", 3_000_000_000n)], true),
        ],
        steps: [stepOf("s-st", "T1", "builder", { role: "implementer" }), stepOf("s-w", "T1", "builder", { role: "implementer" })],
      },
      (container) => {
        const rows = [...container.querySelectorAll(".epic-usage-row")];
        assert.deepEqual(
          rows.map((row) => textOf(row, ".epic-usage-who")),
          ["steering", "T1 implement"],
        );
        assert.deepEqual(rows.map(markOf), ["shared · not in total", null]);
      },
    ));

  await t.test("the head reads Usage, and its sums are the step sessions' alone, the steerer left out", () =>
    drawSection(fixtureInit(false), (container) => {
      assert.equal(textOf(container, ".epic-usage h3"), "Usage");
      assert.equal(textOf(container, ".epic-usage .sec-head span"), "$2.70 at list · billed $2.01");
    }));

  await t.test("a step reads its task and implement when it writes, or review when it is read-only", () =>
    drawSection(
      {
        sessions: [sessionOf("s-w", [totalOf("haiku-5-5", 2_000_000_000n)]), sessionOf("s-r", [totalOf("sonnet-5-5", 1_000_000_000n)])],
        steps: [
          stepOf("s-w", "T1", "builder", { role: "implementer" }),
          stepOf("s-r", "T1", "reviewer", { role: "reviewer", readOnly: true }),
        ],
      },
      (container) => {
        assert.deepEqual(namesOf(container), ["T1 implement", "T1 review"]);
      },
    ));

  await t.test("an evidence judge reads its requirement and judge, and its agent when no requirement holds its run", () =>
    drawSection(
      {
        sessions: [sessionOf("s-j", [totalOf("sonnet-5-5", 2_000_000_000n)]), sessionOf("s-k", [totalOf("sonnet-5-5", 1_000_000_000n)])],
        steps: [
          stepOf("s-j", "evidence-r-1", "judge-agent", { role: "evidence", readOnly: true }),
          stepOf("s-k", "evidence-r-9", "judge-agent", { role: "evidence", readOnly: true }),
        ],
        requirements: [requirementOf("R7", ["r-1"])],
      },
      (container) => {
        assert.deepEqual(namesOf(container), ["R7 judge", "judge-agent"]);
      },
    ));

  await t.test("a step with no task reads its agent", () =>
    drawSection(
      {
        sessions: [sessionOf("s-a", [totalOf("opus-5-5", 1_000_000_000n)])],
        steps: [stepOf("s-a", "", "steelman")],
      },
      (container) => {
        assert.deepEqual(namesOf(container), ["steelman"]);
      },
    ));

  await t.test("a session no step holds reads its label as the epic lists it, else the first 8 characters of its id", () =>
    drawSection(
      {
        sessions: [
          sessionOf("c0ffee12-long-id", [totalOf("opus-5-5", 2_000_000_000n)]),
          sessionOf("d1d1d1d1-other", [totalOf("opus-5-5", 1_000_000_000n)]),
        ],
        epicSessions: [create(EpicSessionSchema, { id: "c0ffee12-long-id", label: "labelled session" })],
      },
      (container) => {
        assert.deepEqual(namesOf(container), ["labelled session", "d1d1d1d1"]);
      },
    ));

  await t.test("each row reads the model of its largest total, and its requests, cache write tokens and list cost", () =>
    drawSection(fixtureInit(false), (container) => {
      assert.deepEqual(rowsOf(container), [
        { name: "steering", model: "opus-5-5", requests: "1,204", cache: "48M", list: "$11.40" },
        { name: "T3 implement", model: "haiku-5-5", requests: "408", cache: "890k", list: "$1.21" },
        { name: "T3 review", model: "sonnet-5-5", requests: "96", cache: "2.4M", list: "$1.05" },
        { name: "steelman", model: "opus-5-5", requests: "44", cache: "900k", list: "$0.23" },
        { name: "R4 judge", model: "sonnet-5-5", requests: "31", cache: "300k", list: "$0.21" },
      ]);
    }));

  await t.test("bars are relative to the largest row, the steerer's, which draws the whole", () =>
    drawSection(fixtureInit(false), (container) => {
      assertWidths(widthsOf(container, ".epic-usage-bar i"), [100, 10.614, 9.211, 2.018, 1.842], 0.005);
    }));

  await t.test("under 760 a row reads its name, model and figures without the bar", async () => {
    let desk: ReturnType<typeof rowsOf> = [];
    await drawSection(fixtureInit(false), (container) => {
      assert.equal(container.querySelectorAll(".epic-usage-bar").length, 5, "the desk draws a bar in each row");
      desk = rowsOf(container);
    });
    await drawSection(fixtureInit(true), (container) => {
      assertNone(container.querySelector(".epic-usage-bar"), "a narrow row drew a bar");
      assert.deepEqual(rowsOf(container), desk, "a narrow row reads the same name, model and figures");
    });
  });

  await t.test("EpicPage reads its usage when it opens, and again when a step ends, and draws the section from each answer", async () => {
    const running = stepOf("s-run", "T2", "builder", { role: "implementer" });
    const reviewing = stepOf("s-rev", "T2", "task-reviewer", { role: "reviewer", readOnly: true });
    const loader = fakeLoader([
      answerOf([sessionOf("s-run", [totalOf("haiku-5-5", 1_000_000_000n)])]),
      answerOf([
        sessionOf("s-run", [totalOf("haiku-5-5", 1_000_000_000n)]),
        sessionOf("s-rev", [totalOf("sonnet-5-5", 500_000_000n)]),
      ]),
    ]);
    await withEpicPage(epicDetail([running, reviewing]), loader.loadUsage, async (container, redraw) => {
      assert.deepEqual(loader.calls, ["e1"], "the usage is read once when the page opens");
      assert.deepEqual(namesOf(container), ["T2 implement"]);
      await redraw(epicDetail([running, reviewing]));
      assert.deepEqual(loader.calls, ["e1"], "a redraw with no step ended reads nothing");
      await redraw(epicDetail([stepOf("s-run", "T2", "builder", { role: "implementer", ended: 9_000n }), reviewing]));
      assert.deepEqual(loader.calls, ["e1", "e1"], "a step ending reads the usage again");
      assert.deepEqual(namesOf(container), ["T2 implement", "T2 review"], "the section draws the later answer");
    });
  });

  await t.test("EpicPage draws no Usage section without loadUsage, nor on an error or an empty answer", async () => {
    const running = stepOf("s-run", "T2", "builder", { role: "implementer" });
    await withEpicPage(epicDetail([running]), undefined, async (container) => {
      assertNone(container.querySelector(".epic-usage"), "a page without loadUsage drew a section");
    });
    const failing = fakeLoader([new Error("usage is unavailable")]);
    await withEpicPage(epicDetail([running]), failing.loadUsage, async (container) => {
      assert.deepEqual(failing.calls, ["e1"], "the usage is read");
      assertNone(container.querySelector(".epic-usage"), "a failed read drew a section");
    });
    const empty = fakeLoader([answerOf([])]);
    await withEpicPage(epicDetail([running]), empty.loadUsage, async (container) => {
      assert.deepEqual(empty.calls, ["e1"], "the usage is read");
      assertNone(container.querySelector(".epic-usage"), "an empty answer drew a section");
    });
  });
});
