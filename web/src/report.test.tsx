import assert from "node:assert/strict";
import { test, type TestContext } from "node:test";

import { type JSX, render } from "preact";
import { act } from "preact/test-utils";

import type { ReportView } from "./ending";
import type { CallOutcome } from "./ownercall";
import { EpicReport } from "./report";
import { installTestDom, removeTestDom } from "./testdom";
import { turn, until } from "./testwait";

// The synthetic epic: made-up slugs and ids only.
const slug = "rota";
const replacements = [
  { id: "widgets-5d0c1a7/lamp", slug: "lamp" },
  { id: "widgets-5d0c1a7/dock", slug: "dock" },
];

// endedView is an epic that ended with gaps and has no report lines yet.
const endedView: ReportView = {
  sentence: "Ended with gaps. It did not release.",
  why: "",
  shipped: "none",
  notShipped: "",
  replacedBy: "",
  gaps: true,
};

// Deferred is a promise its test settles by hand, so a call or a read is held open until the test answers it.
interface Deferred<T> {
  promise: Promise<T>;
  resolve: (value: T) => void;
}

function deferred<T>(): Deferred<T> {
  let resolve: (value: T) => void = () => undefined;
  const promise = new Promise<T>((settle) => {
    resolve = settle;
  });
  return { promise, resolve };
}

// Calls records each call the report makes, in order. Each call is held open until its test answers it, unless the calls
// answer at once: landed for a send, and true for a read, which is what a healthy epic does.
class Calls {
  readonly releases: number[][] = [];
  readonly closes: { kind: string; by: string }[] = [];
  readonly sends: Deferred<CallOutcome>[] = [];
  readonly reads: Deferred<boolean>[] = [];
  private readonly instant: boolean;

  constructor(instant: boolean) {
    this.instant = instant;
  }

  private pending(): Promise<CallOutcome> {
    const answer = deferred<CallOutcome>();
    this.sends.push(answer);
    if (this.instant) {
      answer.resolve({ kind: "landed" });
    }
    return answer.promise;
  }

  release = (undoNone: number[]): Promise<CallOutcome> => {
    this.releases.push(undoNone);
    return this.pending();
  };

  close = (kind: string, by: string): Promise<CallOutcome> => {
    this.closes.push({ kind, by });
    return this.pending();
  };

  reread = (): Promise<boolean> => {
    const read = deferred<boolean>();
    this.reads.push(read);
    if (this.instant) {
      read.resolve(true);
    }
    return read.promise;
  };
}

// withReport gives the check a container on the document, and removes the container and the DOM however the check ends.
async function withReport(check: (container: HTMLElement) => Promise<void>): Promise<void> {
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

interface ReportOptions {
  view?: ReportView;
  narrow?: boolean;
  replacements?: { id: string; slug: string }[];
}

// reportElement is the report for calls and options, as a render would draw it.
function reportElement(calls: Calls, options: ReportOptions): JSX.Element {
  return (
    <EpicReport
      view={options.view ?? endedView}
      slug={slug}
      narrow={options.narrow ?? false}
      replacements={options.replacements ?? replacements}
      onRelease={calls.release}
      onClose={calls.close}
      reread={calls.reread}
    />
  );
}

// draw renders the report into container, and flushes the render.
async function draw(container: HTMLElement, calls: Calls, options: ReportOptions = {}): Promise<void> {
  await act(() => {
    render(reportElement(calls, options), container);
  });
}

// settle waits until check holds, each round running inside act, so the renders an answer causes are applied first.
function settle(t: TestContext, check: () => boolean, what: string): Promise<void> {
  return until(t.signal, check, what, { step: () => act(turn) });
}

// one returns the first element under root that matches selector, which must be there.
function one(root: ParentNode, selector: string): HTMLElement {
  const found = root.querySelector<HTMLElement>(selector);
  assert.ok(found !== null, `no element matches ${selector}`);
  return found;
}

// textOf is the text of the first element under root that matches selector, which must be there.
function textOf(root: ParentNode, selector: string): string {
  return one(root, selector).textContent ?? "";
}

// buttonNamed is the button under root that reads text, which must be drawn.
function buttonNamed(root: ParentNode, text: string): HTMLButtonElement {
  const found = [...root.querySelectorAll<HTMLButtonElement>("button")].find((button) => button.textContent === text);
  assert.ok(found !== undefined, `no button reads ${text}`);
  return found;
}

// radio is the choice of Close as… with the given kind.
function radio(root: ParentNode, kind: string): HTMLInputElement {
  const found = root.querySelector<HTMLInputElement>(`input[type="radio"][value="${kind}"]`);
  assert.ok(found !== null, `no radio for ${kind}`);
  return found;
}

// pick checks a choice of Close as…, the way a reader does: the input is checked and its change is sent.
async function pick(input: HTMLInputElement): Promise<void> {
  await act(() => {
    input.checked = true;
    input.dispatchEvent(new Event("change"));
  });
}

// pickEpic chooses an epic in the superseded select, which picks superseded as well.
async function pickEpic(root: ParentNode, id: string): Promise<void> {
  const select = root.querySelector<HTMLSelectElement>("select");
  assert.ok(select !== null, "no select is drawn");
  await act(() => {
    select.value = id;
    select.dispatchEvent(new Event("change"));
  });
}

// click clicks a control inside act, so the render its click causes has run before the next check.
async function click(control: HTMLElement): Promise<void> {
  await act(() => {
    control.click();
  });
}

// answer resolves the call or read at index with its answer, inside act.
async function answer(held: Deferred<CallOutcome>[], index: number, outcome: CallOutcome): Promise<void> {
  await act(() => {
    held[index].resolve(outcome);
  });
}

// answerRead resolves the read at index with its answer, inside act.
async function answerRead(reads: Deferred<boolean>[], index: number, value: boolean): Promise<void> {
  await act(() => {
    reads[index].resolve(value);
  });
}

// lineText is the text of the line under the group's controls, or null when none is drawn.
function lineText(container: HTMLElement): string | null {
  return container.querySelector(".call-line")?.textContent ?? null;
}

// isIdle is whether the group has no open confirm and no control disabled: it is back at rest.
function isIdle(container: HTMLElement): boolean {
  const buttons = [...container.querySelectorAll<HTMLButtonElement>(".gaps-acts button")];
  return container.querySelector(".sure") === null && buttons.every((button) => !button.disabled);
}

test("EpicReport: the sentence, why, both doors and the replacement are drawn from the view", () =>
  withReport(async (container) => {
    await draw(container, new Calls(false), {
      view: {
        sentence: "Closed as superseded.",
        why: "The lamp epic took over the work.",
        shipped: "rota-1, rota-2",
        notShipped: "rota-3",
        replacedBy: "lamp",
        gaps: false,
      },
    });
    assert.equal(textOf(container, 'section.report[aria-label="Report"] > .sec > .name'), "Report");
    assert.equal(textOf(container, ".now-sentence"), "Closed as superseded.");
    assert.equal(textOf(container, ".report-why"), "The lamp epic took over the work.");
    assert.equal(textOf(container, ".door.shipped"), "shipped rota-1, rota-2");
    assert.equal(textOf(container, ".door.not-shipped"), "not shipped rota-3");
    assert.equal(textOf(container, ".replaced"), "Superseded by lamp");
  }));

test("EpicReport: a report that shipped nothing draws the shipped door as none, and no empty lines", () =>
  withReport(async (container) => {
    await draw(container, new Calls(false), {
      view: { sentence: "Ended with gaps. It did not release.", why: "", shipped: "none", notShipped: "", replacedBy: "", gaps: true },
    });
    assert.equal(textOf(container, ".door.shipped"), "shipped none");
    assert.equal(container.querySelector(".door.not-shipped"), null);
    assert.equal(container.querySelector(".report-why"), null);
    assert.equal(container.querySelector(".replaced"), null);
  }));

test("EpicReport: no controls without gaps", () =>
  withReport(async (container) => {
    await draw(container, new Calls(false), {
      view: { sentence: "Done.", why: "", shipped: "rota-1", notShipped: "", replacedBy: "", gaps: false },
    });
    assert.equal(container.querySelector(".gaps-acts"), null);
    assert.equal(container.querySelector("button"), null);
  }));

test("EpicClose: Release it anyway asks first, sends one release with no undo, and Keep sends nothing", (t) =>
  withReport(async (container) => {
    const calls = new Calls(false);
    await draw(container, calls);
    await click(buttonNamed(container, "Release it anyway"));
    assert.equal(calls.releases.length, 0, "asking sends nothing");
    assert.equal(textOf(container, ".sure"), "Release rota anyway?");
    assert.equal(container.querySelectorAll(".sure")[1]?.textContent, "Its gaps stay, and its release steps run.");
    await click(buttonNamed(container, "Keep it"));
    assert.equal(container.querySelector(".sure"), null);
    assert.equal(calls.releases.length, 0, "keep sends nothing");

    await click(buttonNamed(container, "Release it anyway"));
    await click(buttonNamed(container, "Release it anyway"));
    assert.deepEqual(calls.releases, [[]], "one confirm sends one release with an empty undo list");
    assert.equal(buttonNamed(container, "Release it anyway").disabled, true);
    assert.equal(lineText(container), "Sending…");
    await answer(calls.sends, 0, { kind: "landed" });
    await settle(t, () => calls.reads.length === 1, "the epic is read again");
    assert.equal(lineText(container), "Sent. Reading the epic again…", "disabled until the read answers");
    await answerRead(calls.reads, 0, true);
    await settle(t, () => isIdle(container), "the group is back at rest");
    assert.equal(container.querySelector(".call-line"), null, "a landed call that was read again leaves no line");
    assert.equal(calls.releases.length, 1);
  }));

test("EpicClose: Close as… confirm is disabled until a kind is picked and, for superseded, an epic", () =>
  withReport(async (container) => {
    await draw(container, new Calls(false));
    await click(buttonNamed(container, "Close as…"));
    assert.equal(one(container, "select option").textContent, "pick the epic");
    const confirm = () => buttonNamed(container, buttonLabel(container));
    assert.equal(confirm().disabled, true, "no kind is picked");
    await pick(radio(container, "superseded"));
    assert.equal(confirm().disabled, true, "superseded needs an epic");
    await pickEpic(container, "widgets-5d0c1a7/dock");
    assert.equal(buttonLabel(container), "Close as superseded");
    assert.equal(confirm().disabled, false);
    await pick(radio(container, "shipped"));
    assert.equal(buttonLabel(container), "Close as shipped");
    assert.equal(confirm().disabled, false);
    await pick(radio(container, "abandoned"));
    assert.equal(buttonLabel(container), "Close as abandoned");
    assert.equal(confirm().disabled, false);
  }));

// buttonLabel is the text of the confirm of Close as…: the primary button in the group.
function buttonLabel(container: HTMLElement): string {
  return textOf(container, ".gaps-acts .act.primary");
}

test("EpicClose: one onClose per confirm, with exactly the picked kind and by, for each kind", (t) =>
  withReport(async (container) => {
    const calls = new Calls(true);
    await draw(container, calls);

    await click(buttonNamed(container, "Close as…"));
    await pick(radio(container, "superseded"));
    await pickEpic(container, "widgets-5d0c1a7/dock");
    await click(buttonNamed(container, "Close as superseded"));
    await settle(t, () => isIdle(container), "the first close is answered");

    await click(buttonNamed(container, "Close as…"));
    await pick(radio(container, "shipped"));
    await click(buttonNamed(container, "Close as shipped"));
    await settle(t, () => isIdle(container), "the second close is answered");

    await click(buttonNamed(container, "Close as…"));
    assert.equal(radio(container, "superseded").checked, false, "a new Close as… starts with no pick");
    await pick(radio(container, "abandoned"));
    await click(buttonNamed(container, "Close as abandoned"));
    await settle(t, () => isIdle(container), "the third close is answered");

    assert.deepEqual(calls.closes, [
      { kind: "superseded", by: "widgets-5d0c1a7/dock" },
      { kind: "shipped", by: "" },
      { kind: "abandoned", by: "" },
    ]);
  }));

test("EpicClose: an epic picked for superseded is not sent when the pick moves to shipped", (t) =>
  withReport(async (container) => {
    const calls = new Calls(true);
    await draw(container, calls);
    await click(buttonNamed(container, "Close as…"));
    await pick(radio(container, "superseded"));
    await pickEpic(container, "widgets-5d0c1a7/dock");
    await pick(radio(container, "shipped"));
    await click(buttonNamed(container, "Close as shipped"));
    await settle(t, () => isIdle(container), "the close is answered");
    assert.deepEqual(calls.closes, [{ kind: "shipped", by: "" }]);
  }));

test("EpicClose: a Close as… kept and asked again starts with no epic picked", () =>
  withReport(async (container) => {
    const calls = new Calls(false);
    await draw(container, calls);
    await click(buttonNamed(container, "Close as…"));
    await pick(radio(container, "superseded"));
    await pickEpic(container, "widgets-5d0c1a7/dock");
    await click(buttonNamed(container, "Keep it"));
    await click(buttonNamed(container, "Close as…"));
    await pick(radio(container, "superseded"));
    assert.equal(container.querySelector<HTMLSelectElement>("select")?.value, "", "the select reads pick the epic");
    assert.equal(buttonNamed(container, buttonLabel(container)).disabled, true, "superseded needs an epic again");
    assert.equal(calls.closes.length, 0);
  }));

test("EpicClose: superseded is disabled, and reads so, with no other epic in the repository", () =>
  withReport(async (container) => {
    await draw(container, new Calls(false), { replacements: [] });
    await click(buttonNamed(container, "Close as…"));
    const superseded = radio(container, "superseded");
    assert.equal(superseded.disabled, true);
    assert.equal(superseded.parentElement?.textContent?.trim(), "no other epic in this repository");
    assert.equal(container.querySelector("select"), null);
  }));

test("EpicClose: keep and presses while sending send nothing, and both controls are disabled", (t) =>
  withReport(async (container) => {
    const calls = new Calls(false);
    await draw(container, calls);
    await click(buttonNamed(container, "Close as…"));
    await pick(radio(container, "abandoned"));
    await click(buttonNamed(container, "Close as abandoned"));
    assert.equal(calls.closes.length, 1);
    for (const control of container.querySelectorAll<HTMLButtonElement>(".gaps-acts button")) {
      assert.equal(control.disabled, true, `${control.textContent} is disabled while sending`);
    }
    await click(buttonNamed(container, "Keep it"));
    await click(buttonNamed(container, "Close as abandoned"));
    assert.equal(calls.closes.length, 1, "a press while sending sends nothing");
    assert.equal(one(container, ".call-line").textContent, "Sending…");

    await answer(calls.sends, 0, { kind: "landed" });
    await settle(t, () => calls.reads.length === 1, "the epic is read again");
    await answerRead(calls.reads, 0, true);
    await settle(t, () => isIdle(container), "the group is back at rest");
    assert.equal(calls.closes.length, 1);
  }));

test("EpicClose: a refusal shows its reason in red, and a second press is a second confirmed call", (t) =>
  withReport(async (container) => {
    const calls = new Calls(false);
    await draw(container, calls);
    await click(buttonNamed(container, "Release it anyway"));
    await click(buttonNamed(container, "Release it anyway"));
    await answer(calls.sends, 0, { kind: "refused", reason: "the tag already exists" });
    await settle(t, () => container.querySelector(".call-line.error") !== null, "the refusal is drawn");
    assert.equal(one(container, ".call-line.error").textContent, "Refused: the tag already exists");
    assert.equal(calls.reads.length, 0, "a refusal is not read again");
    assert.equal(buttonNamed(container, "Release it anyway").disabled, false);

    await click(buttonNamed(container, "Release it anyway"));
    assert.equal(container.querySelector(".sure")?.textContent, "Release rota anyway?", "the second press asks again");
    await click(buttonNamed(container, "Release it anyway"));
    assert.equal(calls.releases.length, 2, "the second confirm sends a second release");
  }));

test("EpicClose: an unanswered call says so, and the read is awaited, then offered again", (t) =>
  withReport(async (container) => {
    const calls = new Calls(false);
    await draw(container, calls);
    await click(buttonNamed(container, "Close as…"));
    await pick(radio(container, "shipped"));
    await click(buttonNamed(container, "Close as shipped"));
    await answer(calls.sends, 0, { kind: "unanswered" });
    await settle(t, () => calls.reads.length === 1, "the epic is read again");
    assert.equal(one(container, ".call-line").textContent, "No answer came: it may have gone through. Reading the epic again…");
    assert.equal(buttonNamed(container, "Close as…").disabled, true, "disabled until the read answers");

    await answerRead(calls.reads, 0, false);
    await settle(t, () => lineText(container) === "No answer came: it may have gone through. The epic could not be read again.", "the failed read is drawn");
    await click(buttonNamed(container, "Read it again"));
    assert.equal(calls.reads.length, 2, "Read it again reads the epic again");
    await answerRead(calls.reads, 1, true);
    await settle(t, () => lineText(container) === "No answer came: it may have gone through.", "the answered read is drawn");
    assert.equal(calls.closes.length, 1, "the unanswered call is not sent again");
  }));

test("EpicClose: narrow, both confirms are in a dialog and the group stays under the page", (t) =>
  withReport(async (container) => {
    const calls = new Calls(true);
    await draw(container, calls, { narrow: true });
    await click(buttonNamed(container, "Release it anyway"));
    const release = one(container, "[role='dialog']");
    assert.equal(release.getAttribute("aria-label"), "Release rota anyway?");
    assert.equal(container.querySelector(".gaps-acts .sure"), null, "no in-place confirm when narrow");
    await click(buttonNamed(release, "Release it anyway"));
    assert.deepEqual(calls.releases, [[]]);
    await settle(t, () => isIdle(container), "the release is answered");

    await click(buttonNamed(container, "Close as…"));
    const close = one(container, "[role='dialog']");
    assert.equal(close.getAttribute("aria-label"), "Close rota as…");
    await pick(radio(close, "abandoned"));
    await click(buttonNamed(close, "Close as abandoned"));
    assert.deepEqual(calls.closes, [{ kind: "abandoned", by: "" }]);
  }));

test("EpicClose: a replacement list that drops the picked epic clears the pick, shows the line and sends nothing", (t) =>
  withReport(async (container) => {
    const calls = new Calls(true);
    await draw(container, calls);
    await click(buttonNamed(container, "Close as…"));
    await pick(radio(container, "superseded"));
    await pickEpic(container, "widgets-5d0c1a7/dock");

    await draw(container, calls, { replacements: [replacements[0]] });
    assert.equal(textOf(container, ".close-gone"), "The epic you picked is no longer listed. Choose again.");
    assert.equal(container.querySelector<HTMLSelectElement>("select")?.value, "", "the pick is cleared");
    assert.equal(radio(container, "superseded").checked, false, "no kind is picked");
    assert.equal(buttonLabel(container), "Close as…");
    assert.equal(buttonNamed(container, buttonLabel(container)).disabled, true, "the confirm is disabled");
    assert.equal(calls.closes.length, 0);

    await pickEpic(container, "widgets-5d0c1a7/lamp");
    assert.equal(container.querySelector(".close-gone"), null, "picking again removes the line");
    await click(buttonNamed(container, "Close as superseded"));
    await settle(t, () => isIdle(container), "the close is answered");
    assert.deepEqual(calls.closes, [{ kind: "superseded", by: "widgets-5d0c1a7/lamp" }]);
  }));

test("EpicClose: a confirm pressed after its picked epic is dropped sends nothing and shows the line", (t) =>
  withReport(async (container) => {
    const calls = new Calls(true);
    await draw(container, calls);
    await click(buttonNamed(container, "Close as…"));
    await pick(radio(container, "superseded"));
    await pickEpic(container, "widgets-5d0c1a7/dock");

    // The list changes and the confirm is pressed before the render's effects run, so the press sees the stale pick.
    render(reportElement(calls, { replacements: [replacements[0]] }), container);
    buttonNamed(container, "Close as superseded").click();
    await act(turn);
    assert.equal(calls.closes.length, 0, "a press with an unlisted pick sends nothing");
    assert.equal(textOf(container, ".close-gone"), "The epic you picked is no longer listed. Choose again.");
    assert.equal(container.querySelector<HTMLSelectElement>("select")?.value, "", "the pick is cleared");
    assert.equal(buttonNamed(container, buttonLabel(container)).disabled, true, "the confirm is disabled");
  }));

test("EpicClose: asking Release it anyway or Close as… closes when the epic stops ending with gaps, and is at rest when offered again", () =>
  withReport(async (container) => {
    const calls = new Calls(false);
    await draw(container, calls);
    await click(buttonNamed(container, "Release it anyway"));
    assert.equal(textOf(container, ".sure"), "Release rota anyway?");
    await draw(container, calls, { view: { ...endedView, gaps: false } });
    assert.ok(container.querySelector(".gaps-acts") === null, "the controls are not drawn without gaps");
    await draw(container, calls);
    assert.ok(container.querySelector(".sure") === null, "offered again, the release confirm is not open");
    assert.equal(buttonNamed(container, "Release it anyway").disabled, false);

    await click(buttonNamed(container, "Close as…"));
    await pick(radio(container, "abandoned"));
    await draw(container, calls, { view: { ...endedView, gaps: false } });
    await draw(container, calls);
    assert.ok(container.querySelector(".sure") === null, "offered again, the close confirm is not open");
    await click(buttonNamed(container, "Close as…"));
    assert.equal(radio(container, "abandoned").checked, false, "a new Close as… starts with no kind picked");
    assert.equal(buttonNamed(container, buttonLabel(container)).disabled, true, "the confirm needs a kind");
    assert.deepEqual(calls.releases, []);
    assert.deepEqual(calls.closes, []);
  }));

test("EpicClose: narrow, a release dialog closes when the epic stops ending with gaps, and is not open when offered again", () =>
  withReport(async (container) => {
    const calls = new Calls(false);
    await draw(container, calls, { narrow: true });
    await click(buttonNamed(container, "Release it anyway"));
    assert.equal(one(container, "[role='dialog']").getAttribute("aria-label"), "Release rota anyway?");
    await draw(container, calls, { narrow: true, view: { ...endedView, gaps: false } });
    assert.ok(container.querySelector("[role='dialog']") === null, "the dialog is gone");
    await draw(container, calls, { narrow: true });
    assert.ok(container.querySelector("[role='dialog']") === null, "offered again, no dialog is open");
    assert.deepEqual(calls.releases, []);
  }));

test("EpicClose: narrow, a sheet whose picked epic is dropped clears the pick, shows the line in the dialog and sends nothing", () =>
  withReport(async (container) => {
    const calls = new Calls(true);
    await draw(container, calls, { narrow: true });
    await click(buttonNamed(container, "Close as…"));
    const sheet = () => one(container, "[role='dialog']");
    await pick(radio(sheet(), "superseded"));
    await pickEpic(sheet(), "widgets-5d0c1a7/dock");

    await draw(container, calls, { narrow: true, replacements: [replacements[0]] });
    assert.equal(textOf(sheet(), ".close-gone"), "The epic you picked is no longer listed. Choose again.");
    assert.equal(sheet().querySelector<HTMLSelectElement>("select")?.value, "", "the pick is cleared");
    assert.equal(buttonNamed(sheet(), "Close as…").disabled, true, "the dialog's confirm is disabled");
    assert.equal(calls.closes.length, 0);
  }));

test("EpicClose: narrow, a Close as… dialog closes when the epic stops ending with gaps, and is not open when offered again", () =>
  withReport(async (container) => {
    const calls = new Calls(false);
    await draw(container, calls, { narrow: true });
    await click(buttonNamed(container, "Close as…"));
    await pick(radio(one(container, "[role='dialog']"), "abandoned"));
    await draw(container, calls, { narrow: true, view: { ...endedView, gaps: false } });
    assert.ok(container.querySelector("[role='dialog']") === null, "the dialog is gone");
    await draw(container, calls, { narrow: true });
    assert.ok(container.querySelector("[role='dialog']") === null, "offered again, no dialog is open");
    assert.deepEqual(calls.closes, []);
  }));
