import assert from "node:assert/strict";
import { test, type TestContext } from "node:test";

import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { render } from "preact";
import { act } from "preact/test-utils";

import { EpicSchema, type EpicsGetResponse, EpicsGetResponseSchema } from "./gen/orrery/v1/epics_pb";
import { endedLead, EndedBar, unsteeredLead } from "./endedbar";
import type { Row } from "./rows";
import type { SteerCalls } from "./steer";
import { installTestDom, removeTestDom } from "./testdom";
import { settledFlag, turn, until } from "./testwait";

// Synthetic rows only: made-up ids, directories and states. The epic is rota in widgets, which has a worktree lamp.
const epic = "widgets-5d0c1a7/rota";
const slug = "rota";
const repository = "/home/owner/code/widgets";
const steeringNow = "sess-steer";
const unansweredText = "The server did not answer, so the move may have gone through. Reading the epic again…";

// rowOf is a row with the given fields, and the others of a live pane's shape.
function rowOf(init: Partial<Row>): Row {
  return {
    tool: "",
    key: "pane:box:%3",
    id: "%3",
    archiveId: "sess-x",
    kind: "pane",
    label: "work",
    project: repository,
    host: "box",
    state: "idle",
    summary: "",
    question: "",
    questionHash: "",
    changed: 0,
    seen: 0,
    unseen: false,
    archived: false,
    archiveKind: "session",
    ...init,
  };
}

// fleet holds the steerer, the project's other live conversations, and rows the menu must leave out.
const fleet: Row[] = [
  rowOf({
    key: "headless:sess-steer",
    id: "sess-steer",
    archiveId: "sess-steer",
    kind: "session",
    label: "Plans the rota",
    state: "running",
    archiveKind: "headless",
  }),
  rowOf({
    key: "pane:box:%11",
    id: "%11",
    archiveId: "sess-lamp",
    label: "Lamp",
    project: `${repository}.worktrees/lamp`,
  }),
  rowOf({
    key: "headless:sess-stopped",
    id: "sess-stopped",
    archiveId: "sess-stopped",
    kind: "session",
    label: "Stopped",
    state: "stopped",
    archiveKind: "headless",
  }),
];

// Deferred is a promise its test settles by hand, so a call is held open until the test answers it.
interface Deferred<T> {
  promise: Promise<T>;
  resolve: (value: T) => void;
  reject: (error: unknown) => void;
}

function deferred<T>(): Deferred<T> {
  let resolve: (value: T) => void = () => undefined;
  let reject: (error: unknown) => void = () => undefined;
  const promise = new Promise<T>((settle, fail) => {
    resolve = settle;
    reject = fail;
  });
  return { promise, resolve, reject };
}

// FakeCalls records each call the bar makes, and holds it open until its test answers it.
class FakeCalls implements SteerCalls {
  readonly moves: { epic: string; session: string; answer: Deferred<void> }[] = [];
  readonly continues: { epic: string; answer: Deferred<string> }[] = [];
  readonly gets: { epic: string; answer: Deferred<EpicsGetResponse> }[] = [];

  move(epicId: string, session: string): Promise<void> {
    const answer = deferred<void>();
    this.moves.push({ epic: epicId, session, answer });
    return answer.promise;
  }

  continue(epicId: string): Promise<string> {
    const answer = deferred<string>();
    this.continues.push({ epic: epicId, answer });
    return answer.promise;
  }

  get(epicId: string): Promise<EpicsGetResponse> {
    const answer = deferred<EpicsGetResponse>();
    this.gets.push({ epic: epicId, answer });
    return answer.promise;
  }
}

// readingOf is the epic as Epics.Get answers it, naming the steering session.
function readingOf(steering: string): EpicsGetResponse {
  return create(EpicsGetResponseSchema, { epic: create(EpicSchema, { steering }) });
}

// withBar gives the check a container on the document, and removes the container and the DOM however the check ends.
async function withBar(check: (container: HTMLElement) => Promise<void>): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  try {
    await check(container);
  } finally {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  }
}

// draw renders the bar into container, and flushes the render.
async function draw(
  container: HTMLElement,
  calls: SteerCalls,
  options: { narrow?: boolean; onSteered?: (session: string) => void; rows?: Row[] } = {},
): Promise<void> {
  await act(() => {
    render(
      <EndedBar
        epic={epic}
        slug={slug}
        repository={repository}
        steering={steeringNow}
        rows={options.rows ?? fleet}
        lead={endedLead(slug)}
        calls={calls}
        onSteered={options.onSteered ?? (() => undefined)}
        narrow={options.narrow ?? false}
      />,
      container,
    );
  });
}

// settle waits until check holds, each round running inside act, so the renders an answer causes are applied before the
// next check. ended, when given, reports that the call check waits for has settled, so a wait that can no longer hold fails
// at once by name. It ends with the test's own signal, so a wait that never holds fails the test by name too.
function settle(t: TestContext, check: () => boolean, what: string, ended?: () => boolean): Promise<void> {
  return until(t.signal, check, what, { ended, step: () => act(turn) });
}

// one returns the first element under root that matches selector, which must be there.
function one(root: ParentNode, selector: string): HTMLElement {
  const found = root.querySelector<HTMLElement>(selector);
  assert.ok(found !== null, `no element matches ${selector}`);
  return found;
}

// all returns the elements under root that match selector, in document order.
function all(root: ParentNode, selector: string): HTMLElement[] {
  return Array.from(root.querySelectorAll<HTMLElement>(selector));
}

// continueButton is the bar's first way on, and steerButton its Steer from another… button.
function continueButton(container: HTMLElement): HTMLElement {
  return one(container, ".ended-bar .act.primary");
}

function steerButton(container: HTMLElement): HTMLElement {
  return one(container, ".ended-bar .act.quiet");
}

// click clicks a control inside act, so the render its click causes has run before the next check.
async function click(control: HTMLElement): Promise<void> {
  await act(() => {
    control.click();
  });
}

test("EndedBar: the bar shows its lead and both buttons, with the menu shut", () =>
  withBar(async (container) => {
    await draw(container, new FakeCalls());

    assert.equal(one(container, ".ended-bar .dock-box.ended").getAttribute("data-state"), "stopped");
    assert.equal(one(container, ".ended-lead").textContent, endedLead(slug));
    assert.equal(continueButton(container).textContent, "Continue in a new conversation");
    assert.equal(steerButton(container).textContent, "Steer from another…");
    assert.equal(continueButton(container).hasAttribute("disabled"), false);
    assert.equal(steerButton(container).hasAttribute("disabled"), false);
    assert.equal(container.querySelector(".steer-menu"), null);
  }));

test("EndedBar: Continue sends one Epics.Continue for the epic, both buttons are disabled until it answers, and the re-read steers to the session it names", (t) =>
  withBar(async (container) => {
    const calls = new FakeCalls();
    const steered: string[] = [];
    await draw(container, calls, { onSteered: (session) => steered.push(session) });

    await click(continueButton(container));
    assert.deepEqual(
      calls.continues.map((next) => next.epic),
      [epic],
    );
    assert.equal(calls.moves.length, 0);
    assert.ok(continueButton(container).hasAttribute("disabled"));
    assert.ok(steerButton(container).hasAttribute("disabled"));

    calls.continues[0].answer.resolve("");
    await settle(t, () => calls.gets.length === 1, "the epic is read again");
    assert.deepEqual(
      calls.gets.map((get) => get.epic),
      [epic],
    );
    assert.ok(continueButton(container).hasAttribute("disabled"));
    assert.deepEqual(steered, []);

    calls.gets[0].answer.resolve(readingOf("sess-new"));
    await settle(t, () => !continueButton(container).hasAttribute("disabled"), "the buttons return");
    assert.deepEqual(steered, ["sess-new"]);
    assert.equal(calls.continues.length, 1);
    assert.equal(steerButton(container).hasAttribute("disabled"), false);
  }));

test("EndedBar: a refused Continue shows orreryd's reason in the bar, the buttons are offered again, and nothing is read or steered", (t) =>
  withBar(async (container) => {
    const calls = new FakeCalls();
    const steered: string[] = [];
    await draw(container, calls, { onSteered: (session) => steered.push(session) });

    await click(continueButton(container));
    const refused = settledFlag(calls.continues[0].answer.promise);
    calls.continues[0].answer.reject(new ConnectError("the epic is not live", Code.FailedPrecondition));
    await settle(t, () => container.querySelector(".ended-bar .steer-note") !== null, "the refusal shows", refused);

    const note = one(container, ".ended-bar .steer-note");
    assert.equal(note.textContent, "the epic is not live");
    assert.ok(note.classList.contains("error"));
    assert.equal(continueButton(container).hasAttribute("disabled"), false);
    assert.equal(steerButton(container).hasAttribute("disabled"), false);
    assert.equal(calls.gets.length, 0);
    assert.deepEqual(steered, []);
  }));

test("EndedBar: an unanswered Continue shows the no-answer note in the bar, keeps both buttons disabled until its one re-read answers, and then offers them again", (t) =>
  withBar(async (container) => {
    const calls = new FakeCalls();
    const steered: string[] = [];
    await draw(container, calls, { onSteered: (session) => steered.push(session) });

    await click(continueButton(container));
    calls.continues[0].answer.reject(new ConnectError("the server is gone", Code.Unavailable));
    await settle(t, () => calls.gets.length === 1, "the epic is read again");
    assert.equal(one(container, ".ended-bar .steer-note").textContent, unansweredText);
    assert.ok(continueButton(container).hasAttribute("disabled"));
    assert.ok(steerButton(container).hasAttribute("disabled"));

    calls.gets[0].answer.resolve(readingOf("sess-lamp"));
    await settle(t, () => container.querySelector(".ended-bar .steer-note") === null, "the note clears");
    assert.equal(continueButton(container).hasAttribute("disabled"), false);
    assert.equal(steerButton(container).hasAttribute("disabled"), false);
    assert.equal(calls.continues.length, 1);
    assert.equal(calls.gets.length, 1);
    assert.deepEqual(steered, []);
  }));

test("EndedBar: Steer from another… opens the menu in the anchor at the desk, and its rows are disabled while Continue's call runs", (t) =>
  withBar(async (container) => {
    const calls = new FakeCalls();
    await draw(container, calls);

    await click(steerButton(container));
    const menu = one(container, ".steer-anchor .steer-menu");
    assert.equal(menu.getAttribute("aria-label"), "Steer rota from");
    assert.equal(container.querySelector(".sheet"), null);
    assert.equal(one(menu, ".steer-row.new").hasAttribute("disabled"), false);
    assert.equal(one(menu, ".steer-row[data-session='sess-lamp']").hasAttribute("disabled"), false);

    await click(continueButton(container));
    assert.equal(calls.continues.length, 1);
    assert.ok(all(menu, ".steer-row").every((row) => row.hasAttribute("disabled")));

    calls.continues[0].answer.resolve("");
    await settle(t, () => calls.gets.length === 1, "the epic is read again");
    calls.gets[0].answer.resolve(readingOf("sess-lamp"));
    await settle(t, () => !one(menu, ".steer-row.new").hasAttribute("disabled"), "the rows return");
  }));

test("EndedBar: narrow, Steer from another… opens the menu in a dialog titled Steer rota from, and the anchor holds no menu", () =>
  withBar(async (container) => {
    await draw(container, new FakeCalls(), { narrow: true });

    await click(steerButton(container));
    const dialog = one(container, ".ended-bar [role='dialog']");
    assert.equal(dialog.getAttribute("aria-label"), "Steer rota from");
    assert.ok(dialog.querySelector(".steer-menu") !== null);
    assert.equal(container.querySelector(".steer-anchor .steer-menu"), null);
  }));

test("EndedBar: endedLead and unsteeredLead give the bar's words for the epic's slug", () => {
  assert.equal(
    endedLead("rota"),
    "This conversation has ended, and rota still runs. Continue it in a new conversation, which starts with the contract and the epic’s log, or move it to one that is running.",
  );
  assert.equal(
    unsteeredLead("rota"),
    "No conversation steers rota. Continue it in a new conversation, which starts with the contract and the epic’s log, or move it to one that is running.",
  );
});
