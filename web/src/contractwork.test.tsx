import assert from "node:assert/strict";
import { test, type TestContext } from "node:test";

import { create } from "@bufbuild/protobuf";
import { createClient, createRouterTransport } from "@connectrpc/connect";
import { type JSX, render } from "preact";
import { act } from "preact/test-utils";

import { ContractRecord, type ContractRecordProps } from "./contractrecord";
import { ContractWork } from "./contractwork";
import { tideAccepted, tideProposed } from "./fixtures/contractrecords";
import {
  ContractRequirementTaskSchema,
  ContractRequirementWorkSchema,
  Contracts,
  type ContractsGetResponse,
} from "./gen/orrery/v1/contracts_pb";
import { EvidenceRunSchema } from "./gen/orrery/v1/epics_pb";
import { installTestDom, removeTestDom } from "./testdom";
import { turn, until } from "./testwait";

// withWork renders the element into a fresh test DOM, runs the check on its container, and removes the DOM.
async function withWork(element: JSX.Element, check: (container: HTMLElement) => void): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(element, container);
    check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

// all returns the elements under root that match selector, in document order.
function all(root: ParentNode, selector: string): HTMLElement[] {
  return Array.from(root.querySelectorAll<HTMLElement>(selector));
}

// textOf returns the text of the first element under root that matches selector, which must be there.
function textOf(root: ParentNode, selector: string): string {
  const found = root.querySelector(selector);
  assert.ok(found !== null, `no element matches ${selector}`);
  return found.textContent.trim();
}

// requirementOf returns the drawn requirement whose id is id, which must be there.
function requirementOf(root: ParentNode, id: string): HTMLElement {
  const found = root.querySelector<HTMLElement>(`[data-requirement="${id}"]`);
  assert.ok(found !== null, `no requirement ${id}`);
  return found;
}

// cellsOf returns the text of each cell of a row, in order.
function cellsOf(row: HTMLElement): string[] {
  return Array.from(row.children, (cell) => cell.textContent.trim());
}

// greenRun is a green evidence run that passed, as serve gives the proof of a proven requirement.
function greenRun(id: string) {
  return create(EvidenceRunSchema, { id, phase: "green", state: "ended", passed: true, verdict: "holds", exit: 0 });
}

// task is a task that covers a requirement, with the status and place serve gives it.
function task(id: string, title: string, status: string, place: string) {
  return create(ContractRequirementTaskSchema, { id, title, status, place });
}

// The requirements of the tests: one in each state serve names, served in an order that is not their id order, so the
// check is that the work is drawn in the order it is served.
const requirements = [
  create(ContractRequirementWorkSchema, { id: "R3", text: "One table makes one alert.", state: "not started" }),
  create(ContractRequirementWorkSchema, {
    id: "R1",
    text: "A changed table alerts the office within a minute.",
    state: "proven",
    tasks: [task("T1", "watch-publish", "done", "integrated")],
    proof: greenRun("E1"),
  }),
  create(ContractRequirementWorkSchema, {
    id: "R2",
    text: "An unchanged table sends nothing.",
    state: "being worked",
    tasks: [task("T2", "compare-hash", "open", "implement")],
  }),
  create(ContractRequirementWorkSchema, {
    id: "R4",
    text: "The alert names the table’s own port.",
    state: "failed",
    tasks: [task("T4", "port-from-table", "gap", "green")],
  }),
];

test("ContractWork: each requirement draws its id, text and state words, in the order served and the state's tone", async () => {
  await withWork(<ContractWork work={requirements} epic={null} />, (container) => {
    assert.deepEqual(
      all(container, "[data-requirement]").map((row) => row.getAttribute("data-requirement")),
      ["R3", "R1", "R2", "R4"],
    );
    const expected = [
      { id: "R3", text: "One table makes one alert.", state: "not started", tone: "" },
      { id: "R1", text: "A changed table alerts the office within a minute.", state: "proven", tone: "ok" },
      { id: "R2", text: "An unchanged table sends nothing.", state: "being worked", tone: "live" },
      { id: "R4", text: "The alert names the table’s own port.", state: "failed", tone: "bad" },
    ];
    for (const want of expected) {
      const row = requirementOf(container, want.id);
      assert.equal(textOf(row, "code"), want.id);
      assert.equal(textOf(row, ".strong"), want.text);
      assert.equal(textOf(row, ".chip"), want.state);
      const chip = row.querySelector(".chip");
      assert.ok(chip !== null);
      assert.deepEqual(
        [...chip.classList].filter((name) => name !== "chip"),
        want.tone === "" ? [] : [want.tone],
        `the ${want.state} chip draws its tone`,
      );
    }
  });
});

test("ContractWork: each requirement's tasks draw their id, title, status and place under it, and one with none says so", async () => {
  await withWork(<ContractWork work={requirements} epic={null} />, (container) => {
    const tasks = all(requirementOf(container, "R1"), ".work-tasks .tr");
    assert.deepEqual(tasks.map(cellsOf), [["T1", "watch-publish", "done", "integrated"]]);
    assert.deepEqual(
      all(requirementOf(container, "R2"), ".work-tasks .tr").map(cellsOf),
      [["T2", "compare-hash", "open", "implement"]],
    );
    assert.deepEqual(
      all(requirementOf(container, "R4"), ".work-tasks .tr").map(cellsOf),
      [["T4", "port-from-table", "gap", "green"]],
    );
    const bare = requirementOf(container, "R3");
    assert.equal(bare.querySelector(".work-tasks"), null);
    assert.equal(textOf(bare, ".inote"), "no task yet");
  });
});

test("ContractWork: a requirement's proof draws its run's phase and result, and one with no run reads no proof yet", async () => {
  await withWork(<ContractWork work={requirements} epic={null} />, (container) => {
    assert.equal(textOf(requirementOf(container, "R1"), ".work-proof"), "green · passed");
    assert.equal(textOf(requirementOf(container, "R3"), ".work-proof"), "no proof yet");
    assert.equal(textOf(requirementOf(container, "R2"), ".work-proof"), "no proof yet");
  });
});

test("ContractWork: the epic's door comes first and names the epic, and a press calls its onOpen once", async () => {
  let opened = 0;
  const epic = { id: "tidepool/tide-alerts", onOpen: () => (opened += 1) };
  await withWork(<ContractWork work={requirements} epic={epic} />, (container) => {
    const [first] = Array.from(container.querySelector("section")?.children ?? []);
    assert.ok(first !== undefined && first.classList.contains("work-epic"), "the epic's door is the first thing drawn");
    const door = first.querySelector("button");
    assert.ok(door !== null);
    assert.equal(door.textContent.trim(), "its epic, tide-alerts");
    door.click();
    assert.equal(opened, 1);
  });
});

test("ContractWork: with no epic no door is drawn, and the requirements still are", async () => {
  await withWork(<ContractWork work={requirements} epic={null} />, (container) => {
    assert.equal(container.querySelector(".work-epic"), null);
    assert.equal(container.querySelector("button"), null);
    assert.equal(all(container, "[data-requirement]").length, 4);
  });
});

const recordId = "tidepool/tide-alerts";

// withWorkGet is a Contracts.Get response that carries the requirements' work, as serve gives an accepted contract with work.
function withWorkGet(response: ContractsGetResponse): ContractsGetResponse {
  return { ...response, work: requirements };
}

// drawRecord draws the record of tide-alerts, its Contracts.Get answering with response, with the props given over the
// record's defaults. It waits until the contract is drawn, runs check on the record's element, and removes the DOM however
// check ends.
async function drawRecord(
  t: TestContext,
  response: ContractsGetResponse,
  props: Partial<ContractRecordProps>,
  check: (container: HTMLElement) => void | Promise<void>,
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  const transport = createRouterTransport(({ service }) => {
    service(Contracts, {
      get: async () => response,
    });
  });
  const defaults: ContractRecordProps = {
    id: recordId,
    clients: { contracts: createClient(Contracts, transport) },
    writer: (session) => `writer ${session}`,
    hasEpic: false,
    read: new Set(),
    onRead: () => undefined,
    live: true,
  };
  try {
    await act(() => {
      render(<ContractRecord {...defaults} {...props} />, container);
    });
    await until(t.signal, () => container.querySelector(".dline") !== null, "the contract to be drawn", {
      step: () => act(turn),
    });
    await check(container);
  } finally {
    await act(() => {
      render(null, container);
    });
    await removeTestDom(window);
  }
}

test("ContractWork: an accepted contract whose Get carries work opens on its work, and the document follows it on the same page", async (t) => {
  await drawRecord(t, withWorkGet(tideAccepted), { hasEpic: true, onOpenEpic: () => undefined }, (container) => {
    const [first, second] = Array.from(container.querySelector(".contract-record")?.children ?? []);
    assert.ok(first !== undefined && first.classList.contains("contract-work"), "the work is drawn first");
    assert.ok(second !== undefined && second.classList.contains("contract"), "the contract document follows the work");
    assert.equal(container.querySelectorAll(".contract-work").length, 1);
    assert.ok(second.querySelector(".dline h2") !== null, "the document's title is drawn after the work");
    assert.deepEqual(
      all(container, "[data-requirement]").map((row) => row.getAttribute("data-requirement")),
      ["R3", "R1", "R2", "R4"],
    );
  });
});

test("ContractWork: the work's door on the record opens the contract's epic by its id", async (t) => {
  const opened: string[] = [];
  const props = { hasEpic: true, onOpenEpic: (epic: string) => opened.push(epic) };
  await drawRecord(t, withWorkGet(tideAccepted), props, async (container) => {
    const door = container.querySelector<HTMLButtonElement>(".contract-work .work-epic button");
    assert.ok(door !== null, "the work has its epic's door");
    assert.equal(door.textContent.trim(), "its epic, tide-alerts");
    await act(() => {
      door.click();
    });
    assert.deepEqual(opened, [recordId]);
  });
});

test("ContractWork: with no epic on the page the work draws no door, and its requirements still are drawn", async (t) => {
  await drawRecord(t, withWorkGet(tideAccepted), { hasEpic: false, onOpenEpic: undefined }, (container) => {
    assert.ok(container.querySelector(".contract-work") !== null, "the work is drawn");
    assert.equal(container.querySelector(".contract-work .work-epic"), null);
    assert.equal(all(container, "[data-requirement]").length, 4);
  });
});

test("ContractWork: an accepted contract whose Get carries no work draws no work section, and opens on its document", async (t) => {
  await drawRecord(t, tideAccepted, { hasEpic: true, onOpenEpic: () => undefined }, (container) => {
    assert.equal(container.querySelector(".contract-work"), null);
    const [first] = Array.from(container.querySelector(".contract-record")?.children ?? []);
    assert.ok(first !== undefined && first.classList.contains("contract"), "the document is drawn first");
  });
});

test("ContractWork: a proposed contract draws no work section, even when its Get carries work", async (t) => {
  await drawRecord(t, withWorkGet(tideProposed), { hasEpic: true, onOpenEpic: () => undefined }, (container) => {
    assert.equal(container.querySelector(".contract-work"), null);
    const [first] = Array.from(container.querySelector(".contract-record")?.children ?? []);
    assert.ok(first !== undefined && first.classList.contains("contract"), "the document is drawn first");
  });
});
