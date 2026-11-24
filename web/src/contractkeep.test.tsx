import assert from "node:assert/strict";
import { test, type TestContext } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { Code, ConnectError, type Client, createClient, createRouterTransport } from "@connectrpc/connect";
import { render } from "preact";
import { act } from "preact/test-utils";

import { ContractRecord, type ContractRecordProps } from "./contractrecord";
import { readKey } from "./contracts";
import { at } from "./fixtures/clock";
import { tideReady } from "./fixtures/contractrecords";
import {
  Contracts,
  ContractRowWriteSchema,
  type ContractRowWrite,
  type ContractsGetResponse,
  ContractsGetResponseSchema,
  ContractsKeepResponseSchema,
  ContractsStrikeResponseSchema,
  ContractVersionSchema,
} from "./gen/orrery/v1/contracts_pb";
import { type RecordMaker, RecordMakerSchema } from "./gen/orrery/v1/designs_pb";
import { EpicAdditionSchema, EpicContractSchema } from "./gen/orrery/v1/epics_pb";
import { assertNone, installTestDom, removeTestDom } from "./testdom";
import { turn, until } from "./testwait";

const id = "tidepool/tide-alerts";
const key2 = readKey(id, 2n);
const struckWaits = "Accept waits for the next version, which leaves the struck addition out.";
const you: RecordMaker = create(RecordMakerSchema, { kind: "owner_serve" });

// Script is what the fake server answers the calls with. get is given the number of the get, counted from 1. keep and
// strike are called with the call's addition, and a refusal is a thrown ConnectError.
interface Script {
  get?: (call: number) => ContractsGetResponse;
  keep?: () => void;
  strike?: () => void;
}

// Calls counts the Gets the record makes, and the addition each Keep and Strike names, in the order they came.
interface Calls {
  get: number;
  keep: string[];
  strike: string[];
}

// serve is a fake server that answers the Contracts calls as the script says, and counts each call it takes.
function serve(script: Script): { contracts: Client<typeof Contracts>; calls: Calls } {
  const calls: Calls = { get: 0, keep: [], strike: [] };
  const transport = createRouterTransport(({ service }) => {
    service(Contracts, {
      get: async () => {
        calls.get += 1;
        return (script.get ?? (() => tideReady))(calls.get);
      },
      keep: async (request) => {
        calls.keep.push(request.addition);
        script.keep?.();
        return create(ContractsKeepResponseSchema, {});
      },
      strike: async (request) => {
        calls.strike.push(request.addition);
        script.strike?.();
        return create(ContractsStrikeResponseSchema, {});
      },
    });
  });
  return { contracts: createClient(Contracts, transport), calls };
}

// Rig is what a check reaches: the record's DOM, the calls the fake server took, and settle, which waits until check holds
// while each turn lets the record's calls and renders run.
interface Rig {
  container: HTMLElement;
  calls: Calls;
  settle: (check: () => boolean, what: string) => Promise<void>;
}

// rendered draws the record of tide-alerts with the props given on top, waits until its document has drawn, runs check on
// it, and removes the DOM however check ends.
async function rendered(
  t: TestContext,
  script: Script,
  extra: Partial<ContractRecordProps>,
  check: (rig: Rig) => void | Promise<void>,
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  const { contracts, calls } = serve(script);
  const props: ContractRecordProps = {
    id,
    clients: { contracts },
    writer: (session) => `writer ${session}`,
    hasEpic: false,
    read: new Set(),
    onRead: () => undefined,
    live: true,
    ...extra,
  };
  try {
    await act(() => {
      render(<ContractRecord {...props} />, container);
    });
    await until(t.signal, () => container.querySelector(".contract") !== null, "the contract to draw", {
      step: () => act(turn),
    });
    await check({
      container,
      calls,
      settle: (done, what) => until(t.signal, done, what, { step: () => act(turn) }),
    });
  } finally {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  }
}

// all returns the elements under root that match selector, in document order.
function all(root: ParentNode, selector: string): HTMLElement[] {
  return Array.from(root.querySelectorAll<HTMLElement>(selector));
}

// one returns the first element under root that matches selector, which must be there.
function one(root: ParentNode, selector: string): HTMLElement {
  const found = root.querySelector<HTMLElement>(selector);
  assert.ok(found !== null, `no element matches ${selector}`);
  return found;
}

// rowOf returns the row of the contract's body that carries the id, which must be drawn.
function rowOf(root: ParentNode, rowId: string): HTMLElement {
  const found = root.querySelector<HTMLElement>(`[data-row="${rowId}"]`);
  assert.ok(found !== null, `no row ${rowId}`);
  return found;
}

// buttonTexts are the texts of the buttons under root, in order.
function buttonTexts(root: ParentNode): string[] {
  return all(root, "button").map((button) => button.textContent.trim());
}

// button returns the button under root whose text is text, which must be there.
function button(root: ParentNode, text: string): HTMLButtonElement {
  const found = all(root, "button").find((candidate) => candidate.textContent.trim() === text);
  assert.ok(found !== undefined, `no button reads ${text}`);
  return found as HTMLButtonElement;
}

// hasNote reports whether a note under root reads exactly text.
function hasNote(root: ParentNode, text: string): boolean {
  return all(root, ".inote").some((note) => note.textContent.trim() === text);
}

// alertIn is the text of the refusal under root, or "" when none is drawn.
function alertIn(root: ParentNode): string {
  return root.querySelector('[role="alert"]')?.textContent.trim() ?? "";
}

// press clicks the element inside act, so the renders the click makes are applied.
async function press(element: HTMLElement): Promise<void> {
  await act(() => {
    element.click();
  });
}

// writeOf is a row write the record holds at version 2, the version these tests draw, unless the init says otherwise.
function writeOf(init: MessageInitShape<typeof ContractRowWriteSchema>): ContractRowWrite {
  return create(ContractRowWriteSchema, { version: 2n, time: at("@14:50"), maker: you, ...init });
}

// withAdditions is the Get with the additions given in every version's body, which the record draws from its newest.
function withAdditions(get: ContractsGetResponse, additions: MessageInitShape<typeof EpicAdditionSchema>[]): ContractsGetResponse {
  return create(ContractsGetResponseSchema, {
    contract: get.contract,
    versions: get.versions.map((version) => {
      const body = version.contract;
      assert.ok(body !== undefined, "the version holds its contract");
      return create(ContractVersionSchema, {
        ...version,
        contract: create(EpicContractSchema, { ...body, additions: additions.map((addition) => create(EpicAdditionSchema, addition)) }),
      });
    }),
    states: get.states,
    rows: get.rows,
  });
}

// withRows is the Get with the row writes given, which the record reads for the additions and the rows they remove.
function withRows(get: ContractsGetResponse, rows: ContractRowWrite[]): ContractsGetResponse {
  return create(ContractsGetResponseSchema, { contract: get.contract, versions: get.versions, states: get.states, rows });
}

// twoAdditions is the Get of tide-alerts whose newest version holds two additions, A1 and A2, in that order.
const twoAdditions = withAdditions(tideReady, [
  { location: "alerts/send.go", what: "Retry a failed send three times.", why: "The endpoint dropped sends.", cost: "One file." },
  { location: "alerts/log.go", what: "Log each send.", why: "The office asked for a log.", cost: "One line." },
]);

test("ContractPage: Keep and Strike on an addition each call the served RPC once, with A<n> for its position", (t) =>
  rendered(t, { get: () => twoAdditions }, {}, async (rig) => {
    await press(button(rowOf(rig.container, "A2"), "Keep"));
    await rig.settle(() => rig.calls.get === 2 && rig.calls.keep.length === 1, "the Keep to be sent and the record drawn again");
    await press(button(rowOf(rig.container, "A1"), "Strike"));
    await rig.settle(() => rig.calls.get === 3 && rig.calls.strike.length === 1, "the Strike to be sent and the record drawn again");
    assert.deepEqual(rig.calls.keep, ["A2"]);
    assert.deepEqual(rig.calls.strike, ["A1"]);
  }));

test("ContractPage: a kept addition draws kept with who kept it, in place of its Keep, and keeps its Strike", (t) => {
  const get = withRows(twoAdditions, [writeOf({ row: "A1", action: "keep", seq: 1n })]);
  return rendered(t, { get: () => get }, {}, (rig) => {
    const kept = rowOf(rig.container, "A1");
    assert.equal(one(kept, ".decided.kept").textContent, "kept you, in Orrery");
    assert.deepEqual(buttonTexts(kept), ["Strike"]);
    assert.deepEqual(buttonTexts(rowOf(rig.container, "A2")), ["Keep", "Strike"]);
  });
});

test("ContractPage: a row write made at an older version is not drawn on the version the record holds", (t) => {
  const get = withRows(twoAdditions, [writeOf({ version: 1n, row: "A1", action: "keep", seq: 1n })]);
  return rendered(t, { get: () => get }, {}, (rig) => {
    const addition = rowOf(rig.container, "A1");
    assertNone(addition.querySelector(".decided"));
    assert.deepEqual(buttonTexts(addition), ["Keep", "Strike"]);
  });
});

test("ContractPage: the newest write on an addition decides it: a strike after a keep is struck, and a keep after a strike is kept", async (t) => {
  const strikeThenKeep = withRows(twoAdditions, [
    writeOf({ row: "A1", action: "strike", words: "Strike the retry.", seq: 1n }),
    writeOf({ row: "A1", action: "keep", seq: 2n }),
  ]);
  await rendered(t, { get: () => strikeThenKeep }, {}, (rig) => {
    const addition = rowOf(rig.container, "A1");
    assert.equal(one(addition, ".decided").textContent, "kept you, in Orrery");
    assertNone(addition.querySelector(".decided.struck"));
  });
  const keepThenStrike = withRows(twoAdditions, [
    writeOf({ row: "A1", action: "keep", seq: 1n }),
    writeOf({ row: "A1", action: "strike", words: "Strike the retry.", seq: 2n }),
  ]);
  await rendered(t, { get: () => keepThenStrike }, {}, (rig) => {
    const addition = rowOf(rig.container, "A1");
    assert.equal(one(addition, ".decided").textContent, "struck you, in Orrery “Strike the retry.”");
    assertNone(addition.querySelector(".decided.kept"));
  });
});

test("ContractPage: a struck addition draws struck with its words, names the row it takes with it, and holds Accept", async (t) => {
  const struck = withRows(tideReady, [
    writeOf({ row: "A1", action: "strike", words: "Strike the retry.", seq: 1n }),
    writeOf({ row: "R5", action: "remove", depends: "A1", seq: 2n }),
  ]);
  await rendered(t, { get: () => tideReady }, { read: new Set([key2]) }, (rig) => {
    assert.equal(button(rig.container, "Accept").disabled, false, "Accept is open before the strike");
  });
  await rendered(t, { get: () => struck }, { read: new Set([key2]) }, (rig) => {
    const addition = rowOf(rig.container, "A1");
    assert.equal(addition.classList.contains("struck"), true);
    assert.equal(one(addition, ".decided.struck").textContent, "struck you, in Orrery “Strike the retry.”");
    assert.deepEqual(buttonTexts(addition), []);
    assert.equal(one(addition, ".removes").textContent, "R5 goes with it. The Director is removing it as the next version.");
    const removed = rowOf(rig.container, "R5");
    assert.equal(removed.classList.contains("removing"), true);
    assert.equal(one(removed, ".chip.removing").textContent, "being removed");
    assert.equal(button(rig.container, "Accept").disabled, true);
    assert.equal(hasNote(rig.container, struckWaits), true);
  });
});

test("ContractPage: a struck addition with no row removed says the next version is coming without it", (t) => {
  const struck = withRows(tideReady, [writeOf({ row: "A1", action: "strike", seq: 1n })]);
  return rendered(t, { get: () => struck }, { read: new Set([key2]) }, (rig) => {
    assert.equal(
      one(rowOf(rig.container, "A1"), ".removes").textContent,
      "No row goes with it. The next version is coming without it.",
    );
    assertNone(rig.container.querySelector(".tr.removing"));
    assert.equal(button(rig.container, "Accept").disabled, true);
  });
});

test("ContractPage: a refused Keep or Strike draws its message in that addition's row alone", async (t) => {
  const refuse = (message: string) => () => {
    throw new ConnectError(message, Code.FailedPrecondition);
  };
  await rendered(
    t,
    { get: () => twoAdditions, keep: refuse("the addition is not held"), strike: refuse("state: not proposed") },
    {},
    async (rig) => {
      await press(button(rowOf(rig.container, "A1"), "Keep"));
      await rig.settle(() => alertIn(rowOf(rig.container, "A1")) !== "", "the Keep's refusal to be drawn");
      assert.equal(alertIn(rowOf(rig.container, "A1")), "the addition is not held");
      assert.equal(alertIn(rowOf(rig.container, "A2")), "");
      assert.equal(rig.calls.get, 1, "a refused call is not followed by a read");
      await press(button(rowOf(rig.container, "A2"), "Strike"));
      await rig.settle(() => alertIn(rowOf(rig.container, "A2")) !== "", "the Strike's refusal to be drawn");
      assert.equal(alertIn(rowOf(rig.container, "A2")), "state: not proposed");
      assert.equal(alertIn(rowOf(rig.container, "A1")), "");
    },
  );
});

test("ContractPage: the steps that cannot be undone keep their target and undo while an addition is struck", (t) => {
  const struck = withRows(tideReady, [writeOf({ row: "A1", action: "strike", seq: 1n })]);
  return rendered(t, { get: () => struck }, { read: new Set([key2]) }, (rig) => {
    const section = one(rig.container, '[aria-label^="What leaves this machine"]');
    // A step's cells are its number, step, target and undo, in that order.
    const steps = all(section, ".tr:not(.head)").map((row) => {
      const cells = Array.from(row.children, (cell) => cell.textContent);
      return [cells[2], cells[3]];
    });
    assert.deepEqual(steps, [
      ["origin/main", "revert the merge"],
      ["the office’s endpoint", "none"],
    ]);
  });
});
