import assert from "node:assert/strict";
import { test } from "node:test";

import { render } from "preact";
import type { JSX } from "preact";
import { act } from "preact/test-utils";

import type { ReleaseView } from "./ending";
import type { CallOutcome } from "./ownercall";
import { ReleaseTable } from "./release";
import { installTestDom, removeTestDom } from "./testdom";

async function withRender(node: JSX.Element, check: (container: HTMLElement) => void | Promise<void>): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(node, container);
    await check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

type Row = ReleaseView["rows"][number];

// row is one step of a release, drawn as the view gives it, with the parts of a step left empty unless given.
function row(overrides: Partial<Row> & Pick<Row, "position" | "step">): Row {
  return { target: "", undo: "rerun", state: "waiting", tone: "none", reason: "", evidence: "", runs: [], ...overrides };
}

// viewOf is a release view of rows, offering the step at offer (zero for none).
function viewOf(rows: Row[], offer = 0, then = ""): ReleaseView {
  return { words: "", rows, then, offer };
}

// stopped is a release stopped at its second step, which failed with no undo, so Release from step 2 is offered.
function stopped(): ReleaseView {
  return viewOf(
    [
      row({
        position: 1,
        step: "Push main and tag v0.4.0, signed",
        target: "github, tag v0.4.0",
        undo: "none once someone has fetched the tag",
        state: "passed",
        tone: "ok",
      }),
      row({
        position: 2,
        step: "CI green on the tag",
        target: "github actions",
        undo: "none",
        state: "failed",
        tone: "bad",
        reason: "CI is red on the tag.",
      }),
      row({
        position: 3,
        step: "Publish the APK to the release page",
        target: "github release page",
        undo: "delete the release asset",
        state: "waiting",
      }),
    ],
    2,
  );
}

interface Calls {
  releases: number[][];
  reads: number;
}

// fakes are the two callbacks of a release table, with the calls each one takes: answer is the outcome of a release
// call, and read the answer of a read of the epic.
function fakes(
  answer: (undoNone: number[]) => Promise<CallOutcome>,
  read: () => Promise<boolean>,
): { calls: Calls; onRelease: (undoNone: number[]) => Promise<CallOutcome>; reread: () => Promise<boolean> } {
  const calls: Calls = { releases: [], reads: 0 };
  return {
    calls,
    onRelease: (undoNone) => {
      calls.releases.push(undoNone);
      return answer(undoNone);
    },
    reread: () => {
      calls.reads += 1;
      return read();
    },
  };
}

const landed = (): Promise<CallOutcome> => Promise.resolve({ kind: "landed" });
const reread = (): Promise<boolean> => Promise.resolve(true);

// deferred is a promise its test settles, so the test sees each call while it is still going.
function deferred<T>(): { promise: Promise<T>; resolve: (value: T) => void } {
  let settle: (value: T) => void = () => undefined;
  const promise = new Promise<T>((done) => {
    settle = done;
  });
  return { promise, resolve: (value) => settle(value) };
}

// flushTurns is how many microtask turns a flush takes: each is one promise hop a call's chain of answers makes.
const flushTurns = 10;

// flush lets the settled promises of the owner call run, and Preact draw what they set.
async function flush(): Promise<void> {
  await act(async () => {
    for (let turn = 0; turn < flushTurns; turn += 1) {
      await Promise.resolve();
    }
  });
}

// press clicks an element, and lets the call it starts and Preact draw what it changed.
async function press(element: HTMLElement): Promise<void> {
  await act(() => {
    element.dispatchEvent(new Event("click"));
  });
  await flush();
}

// buttonsNamed reads the buttons under root whose text is the label.
function buttonsNamed(root: ParentNode, label: string): HTMLButtonElement[] {
  return [...root.querySelectorAll<HTMLButtonElement>("button")].filter((button) => button.textContent === label);
}

// buttonNamed reads the one button under root whose text is the label.
function buttonNamed(root: ParentNode, label: string): HTMLButtonElement {
  const found = buttonsNamed(root, label);
  const [only] = found;
  assert.ok(found.length === 1 && only !== undefined, `want one ${label} button, found ${found.length}`);
  return only;
}

// controlOf reads the Release from step control drawn under a step row, which a confirm in a sheet does not match.
function controlOf(container: HTMLElement): HTMLButtonElement {
  const found = container.querySelector<HTMLButtonElement>(".tr-detail > button.act");
  assert.ok(found !== null, "no control under a step row");
  return found;
}

// rowOf reads the step row drawn for a position.
function rowOf(container: HTMLElement, position: number): HTMLElement {
  const found = container.querySelector<HTMLElement>(`.tr[data-step="${position}"]`);
  assert.ok(found !== null, `no row for step ${position}`);
  return found;
}

// detailOf reads the detail drawn under a step row, or null when the row draws none.
function detailOf(container: HTMLElement, position: number): HTMLElement | null {
  const next = rowOf(container, position).nextElementSibling;
  return next instanceof HTMLElement && next.classList.contains("tr-detail") ? next : null;
}

// textsOf reads the text of each element matched under root.
function textsOf(root: ParentNode, selector: string): string[] {
  return [...root.querySelectorAll<HTMLElement>(selector)].map((element) => element.textContent ?? "");
}

// glyphOf reads a cell's glyph, which is empty for a cell with none.
function glyphOf(cell: HTMLElement): string {
  return cell.querySelector('[aria-hidden="true"]')?.textContent ?? "";
}

// callLine reads the line under the controls, which is absent when no call has an answer to show.
function callLine(container: HTMLElement): HTMLElement | null {
  return container.querySelector<HTMLElement>("p.call-line");
}

test("ReleaseTable: each row draws its position, step, target and undo", () =>
  withRender(
    <ReleaseTable
      view={stopped()}
      narrow={false}
      onRelease={landed}
      reread={reread}
    />,
    (container) => {
      const first = rowOf(container, 1);
      assert.equal(first.querySelector(".id")?.textContent, "1");
      assert.equal(first.querySelector(".wide .strong")?.textContent, "Push main and tag v0.4.0, signed");
      assert.equal(first.querySelector(".wide .to")?.textContent, " → github, tag v0.4.0");
      assert.equal(first.querySelector(".undo")?.textContent, "none once someone has fetched the tag");
      const third = rowOf(container, 3);
      assert.equal(third.querySelector(".wide .to")?.textContent, " → github release page");
      assert.equal(third.querySelector(".undo")?.textContent, "delete the release asset");
    },
  ));

test("ReleaseTable: a step with no target draws no target", () =>
  withRender(
    <ReleaseTable
      view={viewOf([row({ position: 1, step: "Merge the epic branch to main", state: "passed", tone: "ok" })])}
      narrow={false}
      onRelease={landed}
      reread={reread}
    />,
    (container) => {
      assert.equal(rowOf(container, 1).querySelector(".to"), null);
      assert.equal(rowOf(container, 1).querySelector(".wide .strong")?.textContent, "Merge the epic branch to main");
    },
  ));

test("ReleaseTable: each state's cell draws its word, tone class and glyph", () =>
  withRender(
    <ReleaseTable
      view={viewOf([
        row({ position: 1, step: "a", state: "waiting", tone: "none" }),
        row({ position: 2, step: "b", state: "running", tone: "live" }),
        row({ position: 3, step: "c", state: "passed", tone: "ok" }),
        row({ position: 4, step: "d", state: "failed", tone: "bad" }),
      ])}
      narrow={false}
      onRelease={landed}
      reread={reread}
    />,
    (container) => {
      const expected: [number, string, string, string][] = [
        [1, "cell none", "waiting", ""],
        [2, "cell live", "running", ""],
        [3, "cell ok", "passed", "✓"],
        [4, "cell bad", "failed", "✗"],
      ];
      for (const [position, cls, word, glyph] of expected) {
        const cell = rowOf(container, position).querySelector<HTMLElement>(".end .cell");
        assert.ok(cell !== null, `no cell for step ${position}`);
        assert.equal(cell.getAttribute("class"), cls);
        assert.equal(glyphOf(cell), glyph);
        assert.equal(cell.textContent, `${glyph}${word}`);
      }
    },
  ));

test("ReleaseTable: a failed step draws its reason, evidence and each run under its row", () =>
  withRender(
    <ReleaseTable
      view={viewOf(
        [
          row({
            position: 2,
            step: "CI green on the tag",
            undo: "rerun",
            state: "failed",
            tone: "bad",
            reason: "CI is red on the tag for the second time.",
            evidence: "run 5519",
            runs: ["attempt 1 · failed · go vet", "attempt 2 · passed"],
          }),
        ],
      )}
      narrow={false}
      onRelease={landed}
      reread={reread}
    />,
    (container) => {
      const detail = detailOf(container, 2);
      assert.ok(detail !== null, "a step with a reason, evidence and runs draws its detail");
      assert.equal(detail.querySelector("p.inote.error")?.textContent, "CI is red on the tag for the second time.");
      assert.equal(detail.querySelector(".log")?.textContent, "run 5519");
      assert.deepEqual(textsOf(detail, "p.run"), ["attempt 1 · failed · go vet", "attempt 2 · passed"]);
    },
  ));

test("ReleaseTable: a step with no reason, evidence or run draws no detail", () =>
  withRender(
    <ReleaseTable
      view={viewOf([row({ position: 1, step: "Merge the epic branch to main", state: "passed", tone: "ok" })])}
      narrow={false}
      onRelease={landed}
      reread={reread}
    />,
    (container) => {
      assert.equal(detailOf(container, 1), null);
    },
  ));

test("ReleaseTable: the Then line draws under the table when there is one", async () => {
  const then = "Then halyard serves rota export, and the harbour office gets this week’s file.";
  await withRender(
    <ReleaseTable view={viewOf([row({ position: 1, step: "a" })], 0, then)} narrow={false} onRelease={landed} reread={reread} />,
    (container) => {
      assert.equal(container.querySelector("p.inote.then")?.textContent, then);
    },
  );
  await withRender(
    <ReleaseTable view={viewOf([row({ position: 1, step: "a" })])} narrow={false} onRelease={landed} reread={reread} />,
    (container) => {
      assert.equal(container.querySelector("p.inote.then"), null);
    },
  );
});

test("ReleaseFrom: no control is drawn when no step is offered", () =>
  withRender(
    <ReleaseTable view={viewOf(stopped().rows, 0)} narrow={false} onRelease={landed} reread={reread} />,
    (container) => {
      assert.deepEqual(buttonsNamed(container, "Release from step 2"), []);
      assert.equal(container.querySelectorAll("button.act").length, 0);
    },
  ));

test("ReleaseFrom: the control is drawn under the offered step and no other", () =>
  withRender(
    <ReleaseTable view={stopped()} narrow={false} onRelease={landed} reread={reread} />,
    (container) => {
      const controls = buttonsNamed(container, "Release from step 2");
      assert.equal(controls.length, 1);
      assert.equal(controls[0]?.closest(".tr-detail")?.previousElementSibling?.getAttribute("data-step"), "2");
      assert.equal(container.querySelectorAll("button.act").length, 1);
    },
  ));

test("ReleaseFrom: a press asks in place on the desk, naming the step and that it cannot be undone", () =>
  withRender(
    <ReleaseTable view={stopped()} narrow={false} onRelease={landed} reread={reread} />,
    async (container) => {
      await press(buttonNamed(container, "Release from step 2"));
      assert.deepEqual(textsOf(container, ".sure"), [
        "Release from step 2?",
        "Step 2, CI green on the tag, cannot be undone.",
      ]);
      assert.equal(container.querySelector('[role="dialog"]'), null);
      assert.equal(buttonNamed(container, "Release from step 2").getAttribute("class"), "act primary");
      assert.equal(buttonNamed(container, "Keep it stopped").getAttribute("class"), "act");
    },
  ));

test("ReleaseFrom: one confirm sends one release of its step, and the sending state holds the controls", () => {
  const answer = deferred<CallOutcome>();
  const read = deferred<boolean>();
  const fake = fakes(() => answer.promise, () => read.promise);
  return withRender(
    <ReleaseTable view={stopped()} narrow={false} onRelease={fake.onRelease} reread={fake.reread} />,
    async (container) => {
      await press(buttonNamed(container, "Release from step 2"));
      assert.deepEqual(fake.calls.releases, []);
      await press(buttonNamed(container, "Release from step 2"));
      assert.deepEqual(fake.calls.releases, [[2]]);
      assert.equal(callLine(container)?.textContent, "Sending…");
      assert.ok(controlOf(container).disabled);
      answer.resolve({ kind: "landed" });
      await flush();
      assert.equal(callLine(container)?.textContent, "Sent. Reading the epic again…");
      assert.ok(controlOf(container).disabled);
      read.resolve(true);
      await flush();
      assert.equal(callLine(container), null);
      assert.equal(controlOf(container).disabled, false);
      assert.deepEqual(fake.calls.releases, [[2]]);
      assert.equal(fake.calls.reads, 1);
    },
  );
});

test("ReleaseFrom: keep sends nothing and draws the control as it was", () => {
  const fake = fakes(landed, reread);
  return withRender(
    <ReleaseTable view={stopped()} narrow={false} onRelease={fake.onRelease} reread={fake.reread} />,
    async (container) => {
      await press(buttonNamed(container, "Release from step 2"));
      await press(buttonNamed(container, "Keep it stopped"));
      assert.deepEqual(fake.calls.releases, []);
      assert.equal(fake.calls.reads, 0);
      assert.deepEqual(textsOf(container, ".sure"), []);
      assert.equal(controlOf(container).disabled, false);
    },
  );
});

test("ReleaseFrom: a press while sending sends nothing", () => {
  const answer = deferred<CallOutcome>();
  const fake = fakes(() => answer.promise, reread);
  return withRender(
    <ReleaseTable view={stopped()} narrow={false} onRelease={fake.onRelease} reread={fake.reread} />,
    async (container) => {
      await press(buttonNamed(container, "Release from step 2"));
      await press(buttonNamed(container, "Release from step 2"));
      await press(buttonNamed(container, "Release from step 2"));
      await press(buttonNamed(container, "Release from step 2"));
      assert.deepEqual(fake.calls.releases, [[2]]);
      answer.resolve({ kind: "landed" });
      await flush();
    },
  );
});

test("ReleaseFrom: a refusal's line is drawn as an error, and a second press asks again", () => {
  const fake = fakes(() => Promise.resolve({ kind: "refused", reason: "the tag v0.4.0 already exists" }), reread);
  return withRender(
    <ReleaseTable view={stopped()} narrow={false} onRelease={fake.onRelease} reread={fake.reread} />,
    async (container) => {
      await press(buttonNamed(container, "Release from step 2"));
      await press(buttonNamed(container, "Release from step 2"));
      assert.deepEqual(fake.calls.releases, [[2]]);
      assert.equal(callLine(container)?.textContent, "Refused: the tag v0.4.0 already exists");
      assert.equal(callLine(container)?.getAttribute("class"), "inote call-line error");
      assert.equal(fake.calls.reads, 0);
      assert.equal(controlOf(container).disabled, false);
      await press(buttonNamed(container, "Release from step 2"));
      assert.deepEqual(textsOf(container, ".sure"), [
        "Release from step 2?",
        "Step 2, CI green on the tag, cannot be undone.",
      ]);
    },
  );
});

test("ReleaseFrom: an unanswered call reads the epic again and holds the controls until the read answers", () => {
  const read = deferred<boolean>();
  const fake = fakes(() => Promise.resolve({ kind: "unanswered" }), () => read.promise);
  return withRender(
    <ReleaseTable view={stopped()} narrow={false} onRelease={fake.onRelease} reread={fake.reread} />,
    async (container) => {
      await press(buttonNamed(container, "Release from step 2"));
      await press(buttonNamed(container, "Release from step 2"));
      assert.deepEqual(fake.calls.releases, [[2]]);
      assert.equal(fake.calls.reads, 1);
      assert.equal(
        callLine(container)?.textContent,
        "No answer came: it may have gone through. Reading the epic again…",
      );
      assert.ok(controlOf(container).disabled);
      read.resolve(false);
      await flush();
      assert.equal(
        callLine(container)?.textContent,
        "No answer came: it may have gone through. The epic could not be read again.",
      );
      assert.ok(controlOf(container).disabled);
      await press(buttonNamed(container, "Read it again"));
      assert.equal(fake.calls.reads, 2);
      assert.deepEqual(fake.calls.releases, [[2]]);
    },
  );
});

test("ReleaseFrom: a read that answers after an unanswered call draws the line and frees the control", () => {
  const read = deferred<boolean>();
  let reads = 0;
  const fake = fakes(() => Promise.resolve({ kind: "unanswered" }), () => {
    reads += 1;
    return reads === 1 ? read.promise : Promise.resolve(true);
  });
  return withRender(
    <ReleaseTable view={stopped()} narrow={false} onRelease={fake.onRelease} reread={fake.reread} />,
    async (container) => {
      await press(buttonNamed(container, "Release from step 2"));
      await press(buttonNamed(container, "Release from step 2"));
      read.resolve(true);
      await flush();
      assert.equal(callLine(container)?.textContent, "No answer came: it may have gone through.");
      assert.equal(controlOf(container).disabled, false);
      assert.deepEqual(fake.calls.releases, [[2]]);
    },
  );
});

test("ReleaseFrom: narrow asks in a dialog that names the step and confirms with one release", () => {
  const fake = fakes(landed, reread);
  return withRender(
    <ReleaseTable view={stopped()} narrow={true} onRelease={fake.onRelease} reread={fake.reread} />,
    async (container) => {
      await press(buttonNamed(container, "Release from step 2"));
      const dialog = container.querySelector<HTMLElement>('[role="dialog"]');
      assert.ok(dialog !== null, "a narrow confirm opens a dialog");
      assert.equal(dialog.getAttribute("aria-label"), "Release from step 2?");
      assert.equal(dialog.querySelector("p")?.textContent, "Step 2, CI green on the tag, cannot be undone.");
      assert.equal(controlOf(container).disabled, false);
      await press(buttonNamed(dialog, "Release from step 2"));
      assert.deepEqual(fake.calls.releases, [[2]]);
    },
  );
});

test("ReleaseFrom: narrow keep closes the dialog and sends nothing", () => {
  const fake = fakes(landed, reread);
  return withRender(
    <ReleaseTable view={stopped()} narrow={true} onRelease={fake.onRelease} reread={fake.reread} />,
    async (container) => {
      await press(buttonNamed(container, "Release from step 2"));
      const dialog = container.querySelector<HTMLElement>('[role="dialog"]');
      assert.ok(dialog !== null, "a narrow confirm opens a dialog");
      await press(buttonNamed(dialog, "Keep it stopped"));
      assert.equal(container.querySelector('[role="dialog"]'), null);
      assert.deepEqual(fake.calls.releases, []);
    },
  );
});

type Fake = ReturnType<typeof fakes>;

// redraw draws the table again in the same container, as a re-read epic does: the view offers the step at offer, zero for none.
async function redraw(container: HTMLElement, offer: number, narrow: boolean, fake: Fake): Promise<void> {
  await act(() => {
    render(
      <ReleaseTable
        view={viewOf(stopped().rows, offer)}
        narrow={narrow}
        onRelease={fake.onRelease}
        reread={fake.reread}
      />,
      container,
    );
  });
  await flush();
}

// confirmOpen reads whether the confirm for step 2 is open: in place on the desk, or as the dialog when narrow.
function confirmOpen(container: HTMLElement, narrow: boolean): boolean {
  return narrow
    ? container.querySelector('[role="dialog"]') !== null
    : container.querySelector(".sure") !== null;
}

// closesWhenOfferMoves opens the confirm for step 2, draws the table as offering next, and checks that the confirm closed
// with no call. Then step 2 is offered again at rest, and only a press and a confirm send its release.
function closesWhenOfferMoves(narrow: boolean, next: number): () => Promise<void> {
  return () => {
    const fake = fakes(landed, reread);
    return withRender(
      <ReleaseTable view={stopped()} narrow={narrow} onRelease={fake.onRelease} reread={fake.reread} />,
      async (container) => {
        await press(buttonNamed(container, "Release from step 2"));
        assert.equal(confirmOpen(container, narrow), true);
        await redraw(container, next, narrow, fake);
        assert.equal(confirmOpen(container, narrow), false);
        assert.deepEqual(buttonsNamed(container, "Release from step 2"), []);
        assert.deepEqual(fake.calls.releases, []);
        assert.equal(fake.calls.reads, 0);
        await redraw(container, 2, narrow, fake);
        assert.equal(confirmOpen(container, narrow), false);
        assert.equal(buttonNamed(container, "Release from step 2").getAttribute("class"), "act");
        assert.deepEqual(fake.calls.releases, []);
        await press(buttonNamed(container, "Release from step 2"));
        assert.equal(confirmOpen(container, narrow), true);
        assert.deepEqual(fake.calls.releases, []);
        const confirm = narrow
          ? buttonNamed(container.querySelector<HTMLElement>('[role="dialog"]') ?? container, "Release from step 2")
          : buttonNamed(container, "Release from step 2");
        await press(confirm);
        assert.deepEqual(fake.calls.releases, [[2]]);
      },
    );
  };
}

test("ReleaseFrom: a desk confirm open for step 2 closes with no call when the offer becomes none", closesWhenOfferMoves(false, 0));

test("ReleaseFrom: a desk confirm open for step 2 closes with no call when the offer becomes step 3", closesWhenOfferMoves(false, 3));

test("ReleaseFrom: a narrow confirm open for step 2 closes with no call when the offer becomes none", closesWhenOfferMoves(true, 0));

test("ReleaseFrom: a narrow confirm open for step 2 closes with no call when the offer becomes step 3", closesWhenOfferMoves(true, 3));

test("ReleaseFrom: a confirm sending when the offer becomes none is not interrupted", () => {
  const answer = deferred<CallOutcome>();
  const fake = fakes(() => answer.promise, reread);
  return withRender(
    <ReleaseTable view={stopped()} narrow={false} onRelease={fake.onRelease} reread={fake.reread} />,
    async (container) => {
      await press(buttonNamed(container, "Release from step 2"));
      await press(buttonNamed(container, "Release from step 2"));
      await redraw(container, 0, false, fake);
      assert.deepEqual(fake.calls.releases, [[2]]);
      answer.resolve({ kind: "landed" });
      await flush();
      assert.equal(fake.calls.reads, 1);
      assert.deepEqual(fake.calls.releases, [[2]]);
    },
  );
});

test("ReleaseFrom: the no-answer line stays when the re-read epic no longer offers the step", () => {
  const fake = fakes(() => Promise.resolve({ kind: "unanswered" }), () => Promise.resolve(true));
  return withRender(
    <ReleaseTable view={stopped()} narrow={false} onRelease={fake.onRelease} reread={fake.reread} />,
    async (container) => {
      await press(buttonNamed(container, "Release from step 2"));
      await press(buttonNamed(container, "Release from step 2"));
      await flush();
      await redraw(container, 0, false, fake);
      assert.deepEqual(buttonsNamed(container, "Release from step 2"), []);
      assert.equal(callLine(container)?.textContent, "No answer came: it may have gone through.");
      assert.deepEqual(fake.calls.releases, [[2]]);
    },
  );
});

test("ReleaseFrom: a refusal's line stays when the epic no longer offers the step", () => {
  const fake = fakes(() => Promise.resolve({ kind: "refused", reason: "the tag already exists" }), () => Promise.resolve(true));
  return withRender(
    <ReleaseTable view={stopped()} narrow={false} onRelease={fake.onRelease} reread={fake.reread} />,
    async (container) => {
      await press(buttonNamed(container, "Release from step 2"));
      await press(buttonNamed(container, "Release from step 2"));
      await flush();
      await redraw(container, 0, false, fake);
      assert.equal(callLine(container)?.textContent, "Refused: the tag already exists");
      assert.equal(callLine(container)?.classList.contains("error"), true);
    },
  );
});
