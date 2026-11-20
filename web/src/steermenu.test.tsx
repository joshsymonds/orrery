import assert from "node:assert/strict";
import { test, type TestContext } from "node:test";

import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { render } from "preact";
import { act } from "preact/test-utils";

import { EpicSchema, type EpicsGetResponse, EpicsGetResponseSchema } from "./gen/orrery/v1/epics_pb";
import type { Row } from "./rows";
import type { SteerCalls } from "./steer";
import { installTestDom, removeTestDom } from "./testdom";
import { settledFlag, turn, until } from "./testwait";
import { type SteerHandle, SteerMenu, useSteer } from "./steermenu";

// Synthetic rows only: made-up ids, directories and states. The epic is rota in widgets, which has a worktree lamp.
const epic = "widgets-5d0c1a7/rota";
const slug = "rota";
const repository = "/home/owner/code/widgets";
const steeringNow = "sess-steer";
const unansweredText = "The server did not answer, so the move may have gone through. Reading the epic again…";

// rowOf is a row with the given fields, and the others of a live pane's shape.
function rowOf(init: Partial<Row>): Row {
  return {
    putOff: false,
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

// fleet holds the steerer, the project's other live conversations, and rows the menu must leave out: another project, a
// stopped headless session, a pane with no orrery id.
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
    key: "headless:sess-other",
    id: "sess-other",
    archiveId: "sess-other",
    kind: "session",
    label: "Checks the rota",
    state: "running",
    archiveKind: "headless",
  }),
  rowOf({ key: "pane:box:%12", id: "%12", archiveId: "sess-orchard", label: "Orchard", project: "/home/owner/code/orchard" }),
  rowOf({
    key: "headless:sess-stopped",
    id: "sess-stopped",
    archiveId: "sess-stopped",
    kind: "session",
    label: "Stopped",
    state: "stopped",
    archiveKind: "headless",
  }),
  rowOf({ key: "pane:box:%13", id: "%13", archiveId: "", label: "Bare", archiveKind: "none" }),
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

// FakeCalls records each call the menu makes, and holds it open until its test answers it.
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

// Harness draws the menu over useSteer, as a host does: the epic's steering and rows come from its caller. onRender
// receives the handle of each render, so a test can make a choice the rows would not let a click make.
function Harness({
  calls,
  rows,
  onSteered,
  onClose,
  onRender,
}: {
  calls: SteerCalls;
  rows: Row[];
  onSteered: (session: string) => void;
  onClose: () => void;
  onRender: (steer: SteerHandle) => void;
}) {
  const steer = useSteer(epic, calls, onSteered);
  onRender(steer);
  return (
    <SteerMenu slug={slug} steering={steeringNow} repository={repository} rows={rows} steer={steer} onClose={onClose} />
  );
}

// withMenu gives the check a container on the document, and removes the container and the DOM however the check ends.
async function withMenu(check: (container: HTMLElement) => Promise<void>): Promise<void> {
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

// draw renders the menu over a fresh useSteer into container, and flushes the render. It returns the handle of the latest
// render, which a test chooses through when a click on a disabled row would not reach it.
async function draw(
  container: HTMLElement,
  calls: SteerCalls,
  onSteered: (session: string) => void = () => undefined,
  onClose: () => void = () => undefined,
  rows: Row[] = fleet,
): Promise<() => SteerHandle> {
  let latest: SteerHandle | null = null;
  await act(() => {
    render(
      <Harness
        calls={calls}
        rows={rows}
        onSteered={onSteered}
        onClose={onClose}
        onRender={(steer) => {
          latest = steer;
        }}
      />,
      container,
    );
  });
  return () => {
    if (latest === null) {
      throw new Error("the menu has not rendered");
    }
    return latest;
  };
}

// choose makes a choice through the handle inside act, as a press that lands before the rows re-render would.
async function choose(handle: () => SteerHandle, session: string | null): Promise<void> {
  await act(() => {
    handle().choose(session);
  });
}

// settle waits until check holds, each round running inside act, so the renders an answer causes are applied before the
// next check. It ends with the test's own signal, so a wait that never holds fails the test by name.
function settle(t: TestContext, check: () => boolean, what: string): Promise<void> {
  return until(t.signal, check, what, { step: () => act(turn) });
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

// rowsOf are the menu's row buttons, the current steerer's row and each choice.
function rowsOf(container: HTMLElement): HTMLElement[] {
  return all(container, ".steer-menu .steer-row");
}

// click clicks a control inside act, so the render its click causes has run before the next check.
async function click(control: HTMLElement): Promise<void> {
  await act(() => {
    control.click();
  });
}

// rowBySession is the menu's row for a session id.
function rowBySession(container: HTMLElement, session: string): HTMLElement {
  return one(container, `.steer-menu .steer-row[data-session="${session}"]`);
}

test("SteerMenu: the menu draws its head, the steerer marked and not choosable, the project's other live conversations, and the new-conversation row", () =>
  withMenu(async (container) => {
    await draw(container, new FakeCalls());

    const menu = one(container, ".steer-menu");
    assert.equal(menu.getAttribute("role"), "menu");
    assert.equal(menu.getAttribute("aria-label"), "Steer rota from");
    assert.equal(one(menu, ".menu-head").textContent, "Steer rota from");

    const current = one(menu, ".steer-row.current");
    assert.equal(current.getAttribute("data-session"), "sess-steer");
    assert.equal(current.getAttribute("aria-current"), "true");
    assert.ok(current.hasAttribute("disabled"));
    assert.equal(one(current, ".t").textContent, "Plans the rota");
    assert.equal(one(current, ".s").textContent, "steers it now");

    const others = all(menu, ".steer-row:not(.current):not(.new)");
    assert.deepEqual(
      others.map((row) => row.getAttribute("data-session")),
      ["sess-lamp", "sess-other"],
    );
    assert.equal(one(others[0], ".t").textContent, "Lamp");
    assert.equal(one(others[0], ".s").textContent, "lamp · idle");
    assert.equal(one(others[1], ".s").textContent, "widgets · running");

    const fresh = one(menu, ".steer-row.new");
    assert.equal(one(fresh, ".t").textContent, "A new conversation in widgets");
    assert.equal(one(fresh, ".s").textContent, "starts with the contract and the epic’s log");
  }));

test("SteerMenu: a row sends one move with the epic and its session, the rows wait on its answer, and a click meanwhile sends nothing", (t) =>
  withMenu(async (container) => {
    const calls = new FakeCalls();
    const steered: string[] = [];
    const steer = await draw(container, calls, (session) => steered.push(session));

    await click(rowBySession(container, "sess-lamp"));
    assert.deepEqual(
      calls.moves.map(({ epic: id, session }) => ({ epic: id, session })),
      [{ epic, session: "sess-lamp" }],
    );
    assert.equal(calls.continues.length, 0);
    assert.ok(rowsOf(container).every((row) => row.hasAttribute("disabled")));

    await click(rowBySession(container, "sess-lamp"));
    await click(rowBySession(container, "sess-other"));
    await click(one(container, ".steer-row.new"));
    await choose(steer, "sess-other");
    await choose(steer, null);
    assert.equal(calls.moves.length, 1);
    assert.equal(calls.continues.length, 0);

    calls.moves[0].answer.resolve();
    await settle(t, () => calls.gets.length === 1, "the epic is read again");
    assert.deepEqual(calls.gets.map((get) => get.epic), [epic]);
    assert.ok(rowsOf(container).every((row) => row.hasAttribute("disabled")));

    calls.gets[0].answer.resolve(readingOf("sess-lamp"));
    await settle(t, () => !one(container, ".steer-row.new").hasAttribute("disabled"), "the rows return");
    assert.deepEqual(steered, ["sess-lamp"]);
    assert.equal(one(container, ".steer-row.current").getAttribute("data-session"), "sess-lamp");
  }));

test("SteerMenu: a new conversation sends one continue, and an empty answer still reads the epic and steers to the session it names", (t) =>
  withMenu(async (container) => {
    const calls = new FakeCalls();
    const steered: string[] = [];
    await draw(container, calls, (session) => steered.push(session));

    await click(one(container, ".steer-row.new"));
    assert.deepEqual(calls.continues.map((next) => next.epic), [epic]);
    assert.equal(calls.moves.length, 0);

    calls.continues[0].answer.resolve("");
    await settle(t, () => calls.gets.length === 1, "the epic is read again");
    calls.gets[0].answer.resolve(readingOf("sess-new"));
    await settle(t, () => !one(container, ".steer-row.new").hasAttribute("disabled"), "the rows return");
    assert.deepEqual(steered, ["sess-new"]);
  }));

test("SteerMenu: a move whose re-read fails steers to the session it moved to, and the rows return", (t) =>
  withMenu(async (container) => {
    const calls = new FakeCalls();
    const steered: string[] = [];
    await draw(container, calls, (session) => steered.push(session));

    await click(rowBySession(container, "sess-other"));
    calls.moves[0].answer.resolve();
    await settle(t, () => calls.gets.length === 1, "the epic is read again");
    calls.gets[0].answer.reject(new ConnectError("the epic is not there", Code.NotFound));
    await settle(t, () => !one(container, ".steer-row.new").hasAttribute("disabled"), "the rows return");
    assert.deepEqual(steered, ["sess-other"]);
  }));

test("SteerMenu: a continue that answers empty, whose re-read fails, steers nowhere and the rows return", (t) =>
  withMenu(async (container) => {
    const calls = new FakeCalls();
    const steered: string[] = [];
    await draw(container, calls, (session) => steered.push(session));

    await click(one(container, ".steer-row.new"));
    calls.continues[0].answer.resolve("");
    await settle(t, () => calls.gets.length === 1, "the epic is read again");
    calls.gets[0].answer.reject(new ConnectError("the epic is not there", Code.NotFound));
    await settle(t, () => !one(container, ".steer-row.new").hasAttribute("disabled"), "the rows return");
    assert.deepEqual(steered, []);
  }));

test("SteerMenu: a refusal shows orreryd's reason, the rows are offered again, and nothing is read or steered", (t) =>
  withMenu(async (container) => {
    const calls = new FakeCalls();
    const steered: string[] = [];
    await draw(container, calls, (session) => steered.push(session));

    await click(rowBySession(container, "sess-lamp"));
    calls.moves[0].answer.reject(new ConnectError("the epic is not live", Code.FailedPrecondition));
    await settle(t, () => container.querySelector(".steer-menu .steer-note") !== null, "the refusal shows");

    assert.equal(one(container, ".steer-menu .steer-note").textContent, "the epic is not live");
    assert.ok(one(container, ".steer-menu .steer-note").classList.contains("error"));
    assert.equal(rowBySession(container, "sess-lamp").hasAttribute("disabled"), false);
    assert.equal(one(container, ".steer-row.new").hasAttribute("disabled"), false);
    assert.equal(calls.gets.length, 0);
    assert.deepEqual(steered, []);
  }));

test("SteerMenu: an unanswered move keeps the rows disabled, reads the epic once, and offers the rows with the steerer it reads, a further send needing a click", (t) =>
  withMenu(async (container) => {
    const calls = new FakeCalls();
    const steered: string[] = [];
    await draw(container, calls, (session) => steered.push(session));

    await click(rowBySession(container, "sess-other"));
    calls.moves[0].answer.reject(new ConnectError("the server is gone", Code.Unavailable));
    await settle(t, () => container.querySelector(".steer-menu .steer-note") !== null, "the no-answer note shows");

    assert.equal(one(container, ".steer-menu .steer-note").textContent, unansweredText);
    assert.ok(rowsOf(container).every((row) => row.hasAttribute("disabled")));

    await click(one(container, ".steer-row.new"));
    await click(rowBySession(container, "sess-lamp"));
    assert.equal(calls.moves.length, 1);
    assert.equal(calls.continues.length, 0);
    assert.equal(calls.gets.length, 1);

    calls.gets[0].answer.resolve(readingOf("sess-lamp"));
    await settle(t, () => container.querySelector(".steer-menu .steer-note") === null, "the note clears");
    assert.equal(one(container, ".steer-row.current").getAttribute("data-session"), "sess-lamp");
    assert.equal(one(container, ".steer-row.new").hasAttribute("disabled"), false);
    assert.deepEqual(steered, []);
    assert.equal(calls.moves.length, 1);

    await click(rowBySession(container, "sess-other"));
    assert.equal(calls.moves.length, 2);
    assert.equal(calls.moves[1].session, "sess-other");
  }));

test("SteerMenu: an unanswered move whose re-read also fails keeps the rows disabled and sends nothing more", (t) =>
  withMenu(async (container) => {
    const calls = new FakeCalls();
    const steered: string[] = [];
    const steer = await draw(container, calls, (session) => steered.push(session));

    await click(rowBySession(container, "sess-other"));
    calls.moves[0].answer.reject(new ConnectError("the server is gone", Code.Unavailable));
    await settle(t, () => calls.gets.length === 1, "the epic is read again");
    const readFailed = settledFlag(calls.gets[0].answer.promise);
    calls.gets[0].answer.reject(new ConnectError("the server is gone", Code.Unavailable));
    await settle(t, readFailed, "the re-read fails");
    await act(turn);

    assert.ok(rowsOf(container).every((row) => row.hasAttribute("disabled")));
    await click(one(container, ".steer-row.new"));
    await choose(steer, "sess-lamp");
    assert.equal(calls.moves.length, 1);
    assert.equal(calls.continues.length, 0);
    assert.deepEqual(steered, []);
  }));

test("SteerMenu: an answer that arrives after the menu unmounts reaches onSteered once with the session the read names", (t) =>
  withMenu(async (container) => {
    const calls = new FakeCalls();
    const steered: string[] = [];
    await draw(container, calls, (session) => steered.push(session));

    await click(rowBySession(container, "sess-lamp"));
    calls.moves[0].answer.resolve();
    await settle(t, () => calls.gets.length === 1, "the epic is read again");
    await act(() => {
      render(null, container);
    });
    const readDone = settledFlag(calls.gets[0].answer.promise);
    calls.gets[0].answer.resolve(readingOf("sess-other"));
    await settle(t, readDone, "the read answers");

    assert.deepEqual(steered, ["sess-other"]);
  }));

test("SteerMenu: Escape calls onClose and stops the key inside the menu", () =>
  withMenu(async (container) => {
    const keys: string[] = [];
    container.addEventListener("keydown", (event) => keys.push(event.key));
    let closed = 0;
    await draw(container, new FakeCalls(), undefined, () => {
      closed += 1;
    });

    one(container, ".steer-menu").dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));

    assert.equal(closed, 1);
    assert.deepEqual(keys, []);
  }));
