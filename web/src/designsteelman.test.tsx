import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import {
  DesignLedgerItemSchema,
  DesignStageSchema,
  DesignStepSchema,
  RecordMakerSchema,
  type DesignLedgerItem,
  type DesignStage,
  type RecordMaker,
} from "./gen/orrery/v1/designs_pb";
import { DesignSteelman } from "./designsteelman";
import { assertNone, installTestDom, removeTestDom } from "./testdom";

// stage is a design's stage with the given steps, ledger and budget flags, and nothing else set.
function stage(init: MessageInitShape<typeof DesignStageSchema> = {}): DesignStage {
  return create(DesignStageSchema, init);
}

// step is a step of the design's stage, with the given kind, status, verdict and whether it counts.
function step(init: MessageInitShape<typeof DesignStepSchema>) {
  return create(DesignStepSchema, init);
}

// finding is a ledger item of the design's stage.
function finding(init: MessageInitShape<typeof DesignLedgerItemSchema>): DesignLedgerItem {
  return create(DesignLedgerItemSchema, init);
}

// makerOf is who made a record, with the fields the kind uses.
function makerOf(init: MessageInitShape<typeof RecordMakerSchema>): RecordMaker {
  return create(RecordMakerSchema, init);
}

// discoveryDone is the discovery pass that returned READY and counted.
const discoveryDone = step({ step: "s1", kind: "discovery", subject: "the drafts", status: "done", verdict: "READY", counted: true, created: 1n });

// closureBlocked is a closure pass that came back blocked, so it did not count.
const closureBlocked = step({ step: "s2", kind: "closure", subject: "the revised drafts", status: "blocked", verdict: "BLOCKED", counted: false, created: 2n });

// withSteelman renders a DesignSteelman of stage into a fresh container, runs check on it, and removes it.
async function withSteelman(design: DesignStage, check: (container: HTMLElement) => void): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  try {
    await act(() => {
      render(<DesignSteelman stage={design} />, container);
    });
    check(container);
  } finally {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  }
}

// cellOf returns the budget's cell of pass, and fails when none is drawn for it.
function cellOf(root: ParentNode, pass: "discovery" | "closure"): HTMLElement {
  const found = root.querySelector<HTMLElement>(`[data-cell="${pass}"]`);
  assert.ok(found, `the ${pass} cell is drawn`);
  return found;
}

// cellWords returns the words the budget's cell of pass says.
function cellWords(root: ParentNode, pass: "discovery" | "closure"): string {
  return cellOf(root, pass).querySelector(".ds")?.textContent ?? "";
}

// passLines returns each pass's line, oldest first.
function passLines(root: ParentNode): HTMLElement[] {
  return [...root.querySelectorAll<HTMLElement>("li.ss-pass")];
}

// itemOf returns the ledger item with the given id, and fails when none is drawn for it.
function itemOf(root: ParentNode, id: string): HTMLElement {
  const found = root.querySelector<HTMLElement>(`li.ss-item[data-item="${id}"]`);
  assert.ok(found, `the ledger item ${id} is drawn`);
  return found;
}

// fieldOf returns the text of the ledger item's field with the given label, or null when the item draws no such field.
function fieldOf(item: ParentNode, label: string): string | null {
  const term = [...item.querySelectorAll("dt")].find((dt) => dt.textContent === label);
  return term?.nextElementSibling?.textContent ?? null;
}

test("DesignPage: steelman's budget says 1 left in both cells while no pass has counted", async () => {
  const failed = step({ step: "s1", kind: "discovery", subject: "the drafts", status: "failed", counted: false, created: 1n });
  await withSteelman(stage({ steps: [failed] }), (container) => {
    assert.equal(cellOf(container, "discovery").querySelector(".nm")?.textContent, "Discovery · 1");
    assert.equal(cellOf(container, "closure").querySelector(".nm")?.textContent, "Closure · at most 1");
    assert.equal(cellWords(container, "discovery"), "1 left", "an uncounted discovery cell is not 1 left");
    assert.equal(cellWords(container, "closure"), "1 left", "an unstarted closure cell is not 1 left");
  });
});

test("DesignPage: steelman's budget says running in the cell of the pass that runs", async () => {
  const running = step({ step: "s1", kind: "discovery", subject: "the drafts", status: "running", created: 1n });
  await withSteelman(stage({ steps: [running] }), (container) => {
    assert.equal(cellWords(container, "discovery"), "running", "the running discovery cell does not say running");
    assert.equal(cellWords(container, "closure"), "1 left", "the closure cell is not 1 left while discovery runs");
  });
});

test("DesignPage: steelman's budget says running in closure's cell while closure runs after a counted discovery", async () => {
  const closureRunning = step({ step: "s2", kind: "closure", subject: "the revised drafts", status: "running", counted: false, created: 2n });
  await withSteelman(stage({ steps: [discoveryDone, closureRunning], discoveryCounted: true }), (container) => {
    assert.equal(cellWords(container, "closure"), "running", "the running closure cell does not say running");
    assert.equal(cellWords(container, "discovery"), "used · ready", "the counted discovery cell does not say its verdict");
  });
});

test("DesignPage: steelman's budget says used with the verdict of the pass that counted, in each cell", async () => {
  const discovery = step({ step: "s1", kind: "discovery", subject: "the drafts", status: "done", verdict: "NEEDS_DECISION", counted: true, created: 1n });
  const closure = step({ step: "s2", kind: "closure", subject: "the revised drafts", status: "done", verdict: "STILL_OPEN", counted: true, created: 2n });
  await withSteelman(stage({ steps: [discovery, closure], discoveryCounted: true, closureCounted: true }), (container) => {
    assert.equal(cellWords(container, "discovery"), "used · needs your decision");
    assert.equal(cellWords(container, "closure"), "used · still open");
  });
});

test("DesignPage: steelman's budget says not used · skipped in closure's cell when closure was skipped", async () => {
  await withSteelman(stage({ steps: [discoveryDone], discoveryCounted: true, closureSkipped: true }), (container) => {
    assert.equal(cellWords(container, "closure"), "not used · skipped", "the skipped closure cell does not say so");
    assert.equal(cellWords(container, "discovery"), "used · ready");
  });
});

test("DesignPage: steelman's passes are each discovery and closure step, oldest first, with its kind, status and verdict", async () => {
  await withSteelman(stage({ steps: [discoveryDone, closureBlocked] }), (container) => {
    const lines = passLines(container);
    assert.deepEqual(
      lines.map((line) => line.dataset.step),
      ["s1", "s2"],
      "the passes are not drawn oldest first",
    );
    assert.equal(lines[0].querySelector(".ss-kind")?.textContent, "discovery");
    assert.equal(lines[0].querySelector(".ss-status")?.textContent, "done");
    assert.equal(lines[0].querySelector(".chip")?.textContent, "ready");
    assert.equal(lines[1].querySelector(".ss-kind")?.textContent, "closure");
    assert.equal(lines[1].querySelector(".ss-status")?.textContent, "blocked");
    assert.equal(lines[1].querySelector(".chip")?.textContent, "blocked");
  });
});

test("DesignPage: steelman's passes leave out a research step", async () => {
  const research = step({ step: "r1", kind: "research", subject: "the rota", status: "done", counted: false, created: 0n });
  await withSteelman(stage({ steps: [research, discoveryDone] }), (container) => {
    assert.deepEqual(
      passLines(container).map((line) => line.dataset.step),
      ["s1"],
      "the research step is drawn as a pass",
    );
  });
});

test("DesignPage: steelman's pass that did not count says did not count, and a counted pass does not", async () => {
  await withSteelman(stage({ steps: [discoveryDone, closureBlocked] }), (container) => {
    const [counted, uncounted] = passLines(container);
    assert.equal(uncounted.textContent?.includes("did not count"), true, "the uncounted closure pass does not say it did not count");
    assert.equal(counted.textContent?.includes("did not count"), false, "the counted discovery pass says it did not count");
  });
});

// The four findings the ledger is drawn with: one the conversation adopted for you, one still open, one rejected with your
// words quoted, and one deferred in Orrery to a boundary.
const adopted = finding({
  id: "D1",
  contract: "rota-export",
  origin: "discovery",
  impact: "The export drops the Lisbon rule.",
  evidence: "rota_export.go:88",
  change: "Keep the Lisbon rule in the export.",
  disposition: "adopted",
  maker: makerOf({ kind: "conversation", reason: "it only needs the text changed", delegation: "decide the small ones" }),
});
const open = finding({
  id: "D2",
  contract: "office-alerts",
  origin: "discovery",
  impact: "The alert names a port that left the rota.",
  evidence: "alerts.yaml:14",
  change: "Name the port from the rota.",
  disposition: "open",
});
const rejected = finding({
  id: "D3",
  contract: "change-feed",
  origin: "closure",
  impact: "The feed replays a change after a restart.",
  evidence: "feed.go:211",
  change: "Drop the replayed change by its sequence.",
  disposition: "rejected",
  reason: "the feed is not restarted in the office",
  maker: makerOf({ kind: "owner_conversation", quote: "Restarts do not happen in the office." }),
});
const deferred = finding({
  id: "D4",
  contract: "pilot-push",
  origin: "discovery",
  impact: "The pilot has no release steps.",
  evidence: "pilot-push/plan.md:3",
  change: "Add the release steps to the pilot.",
  disposition: "deferred",
  boundary: "the pilot is cut for the office",
  maker: makerOf({ kind: "owner_serve" }),
});

test("DesignPage: steelman's ledger draws each item's id, origin, impact, change, evidence and disposition", async () => {
  await withSteelman(stage({ steps: [discoveryDone], ledger: [adopted, open, rejected, deferred] }), (container) => {
    const expected: [DesignLedgerItem, string][] = [
      [adopted, "adopted"],
      [open, "open"],
      [rejected, "rejected"],
      [deferred, "deferred"],
    ];
    for (const [item, disposition] of expected) {
      const drawn = itemOf(container, item.id);
      assert.equal(drawn.querySelector(".ss-head code")?.textContent, item.id, `${item.id} is not drawn in code`);
      assert.equal(drawn.querySelector(".ss-impact")?.textContent, item.impact, `${item.id} has no impact`);
      assert.equal(drawn.querySelector(".ss-head .chip")?.textContent, disposition, `${item.id} has no disposition`);
      assert.equal(fieldOf(drawn, "Origin"), item.origin, `${item.id} has no origin`);
      assert.equal(fieldOf(drawn, "Change"), item.change, `${item.id} has no change`);
      assert.equal(drawn.querySelector("dd code")?.textContent, item.evidence, `${item.id} has no evidence in code`);
    }
  });
});

test("DesignPage: steelman's ledger names who decided each item, through whoOfMaker, and names no one for an open item", async () => {
  await withSteelman(stage({ steps: [discoveryDone], ledger: [adopted, open, rejected, deferred] }), (container) => {
    assert.equal(
      itemOf(container, "D1").querySelector(".ss-who > span:last-child")?.textContent,
      "the Director, because it only needs the text changed, under your words “decide the small ones”",
      "the conversation's decision is not named by whoOfMaker",
    );
    assert.equal(
      itemOf(container, "D3").querySelector(".ss-who")?.textContent,
      "you, in the conversation, quoting “Restarts do not happen in the office.”",
    );
    assert.equal(itemOf(container, "D4").querySelector(".ss-who")?.textContent, "you, in Orrery");
    assertNone(itemOf(container, "D2").querySelector(".ss-who"), "an open item names a maker");
  });
});

test("DesignPage: steelman's ledger marks an item the conversation decided as decided for you, with its reason", async () => {
  await withSteelman(stage({ steps: [discoveryDone], ledger: [adopted, rejected, deferred] }), (container) => {
    const marked = itemOf(container, "D1").querySelector(".ss-who .chip");
    assert.equal(marked?.textContent, "decided for you", "the conversation's decision is not marked as decided for you");
    assert.ok(
      itemOf(container, "D1").querySelector(".ss-who")?.textContent?.includes("it only needs the text changed"),
      "the decision does not say its reason",
    );
    assertNone(itemOf(container, "D3").querySelector(".ss-who .chip"), "an item you decided is marked as decided for you");
    assertNone(itemOf(container, "D4").querySelector(".ss-who .chip"), "an item decided in Orrery is marked as decided for you");
  });
});

test("DesignPage: steelman's ledger says a rejected item's reason and a deferred item's boundary, and nothing for an adopted one", async () => {
  await withSteelman(stage({ steps: [discoveryDone], ledger: [adopted, rejected, deferred] }), (container) => {
    assert.equal(itemOf(container, "D3").querySelector(".ss-why")?.textContent, "Rejected because: the feed is not restarted in the office");
    assert.equal(itemOf(container, "D4").querySelector(".ss-why")?.textContent, "Deferred until the pilot is cut for the office");
    assertNone(itemOf(container, "D1").querySelector(".ss-why"), "an adopted item says why");
  });
});

test("DesignPage: steelman's ledger draws closure's word and note once closure gave them, and no closure field before", async () => {
  const closed = finding({
    id: "D5",
    contract: "office-alerts",
    origin: "discovery",
    impact: "The command still runs the old binary.",
    evidence: "alerts.yaml:22",
    change: "Run the alerts binary.",
    disposition: "adopted",
    maker: makerOf({ kind: "conversation", reason: "it only needs the text changed" }),
    closureWord: "still open",
    closureNote: "the command runs pilotpush",
  });
  await withSteelman(stage({ steps: [discoveryDone], ledger: [closed, adopted] }), (container) => {
    assert.equal(fieldOf(itemOf(container, "D5"), "Closure"), "still open · the command runs pilotpush");
    assert.equal(fieldOf(itemOf(container, "D1"), "Closure"), null, "an item closure has not given a word draws a closure field");
  });
});

test("DesignPage: steelman's ledger says it froze when closure started, and says nothing of it while unfrozen", async () => {
  await withSteelman(stage({ steps: [discoveryDone], ledger: [adopted], frozen: true }), (container) => {
    assert.equal(container.querySelector(".ss-ledger .name")?.textContent, "Ledger, frozen");
    assert.equal(container.querySelector(".ss-ledger .rest")?.textContent, "froze when closure started");
  });
  await withSteelman(stage({ steps: [discoveryDone], ledger: [adopted] }), (container) => {
    assert.equal(container.querySelector(".ss-ledger .name")?.textContent, "Ledger");
    assertNone(container.querySelector(".ss-ledger .rest"), "an unfrozen ledger says it froze");
  });
});

test("DesignPage: steelman draws nothing for a stage with no discovery or closure step and no ledger item", async () => {
  const research = step({ step: "r1", kind: "research", subject: "the rota", status: "done", counted: false, created: 0n });
  await withSteelman(stage({ steps: [research] }), (container) => {
    assert.equal(container.innerHTML, "", "a stage with only a research step draws something");
  });
  await withSteelman(stage(), (container) => {
    assert.equal(container.innerHTML, "", "an empty stage draws something");
  });
});
