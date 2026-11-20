import assert from "node:assert/strict";
import { test, type TestContext } from "node:test";

import { create } from "@bufbuild/protobuf";
import { type Client, Code, ConnectError, createClient, createRouterTransport } from "@connectrpc/connect";
import { render } from "preact";
import { act } from "preact/test-utils";

import { type ContractRecordProps, ContractRecord } from "./contractrecord";
import { type RowAbout, readKey } from "./contracts";
import { at } from "./fixtures/clock";
import { tideAccepted, tideProposed, tideReady, tideSetAside } from "./fixtures/contractrecords";
import {
  ContractDecisionSchema,
  ContractSchema,
  ContractVersionSchema,
  Contracts,
  ContractsAcceptResponseSchema,
  type ContractsGetResponse,
  ContractsGetResponseSchema,
  ContractsReopenResponseSchema,
  ContractsSetAsideResponseSchema,
} from "./gen/orrery/v1/contracts_pb";
import { clock } from "./reader";
import { assertNone, installTestDom, removeTestDom } from "./testdom";
import { turn, until } from "./testwait";

const id = "tidepool/tide-alerts";
const key1 = readKey(id, 1n);
const key2 = readKey(id, 2n);
const irreversible = "What leaves this machine or can’t be undone";
const noLive = "Its conversation has ended: you can accept it or set it aside, but not change it.";
const newerNote = "A newer version came in. Read it before you accept.";
const writer = (session: string) => `writer ${session}`;

// Script is what the fake server answers each call with. A call the script does not name answers from the fixture, and a
// refusal is a thrown ConnectError. get is given the number of the get, counted from 1.
interface Script {
  get?: (call: number) => ContractsGetResponse | Promise<ContractsGetResponse>;
  accept?: (words: string) => Promise<void> | void;
  setAside?: (words: string) => Promise<void> | void;
  reopen?: (words: string) => Promise<void> | void;
}

// Calls counts the calls the record makes, and each write's words in the order the writes came.
interface Calls {
  get: number;
  accept: string[];
  setAside: string[];
  reopen: string[];
}

// serve is a fake server that answers the Contracts calls as the script says, and counts each call it takes.
function serve(script: Script): { contracts: Client<typeof Contracts>; calls: Calls } {
  const calls: Calls = { get: 0, accept: [], setAside: [], reopen: [] };
  const transport = createRouterTransport(({ service }) => {
    service(Contracts, {
      get: async () => {
        calls.get += 1;
        return (script.get ?? (() => tideReady))(calls.get);
      },
      accept: async (request) => {
        calls.accept.push(request.words);
        await script.accept?.(request.words);
        return create(ContractsAcceptResponseSchema, {});
      },
      setAside: async (request) => {
        calls.setAside.push(request.words);
        await script.setAside?.(request.words);
        return create(ContractsSetAsideResponseSchema, {});
      },
      reopen: async (request) => {
        calls.reopen.push(request.words);
        await script.reopen?.(request.words);
        return create(ContractsReopenResponseSchema, {});
      },
    });
  });
  return { contracts: createClient(Contracts, transport), calls };
}

// Setup is what a test gives the record besides the fake server: what the viewer has read, what the conversation allows,
// and the callbacks the test counts.
interface Setup {
  script?: Script;
  live?: boolean;
  hasEpic?: boolean;
  read?: string[];
  onRead?: (key: string) => void;
  onAbout?: (about: RowAbout) => void;
  aboutId?: string;
  onChanged?: () => void;
  onTalk?: () => void;
}

// Rig is what a check reaches: the record's DOM, the calls the fake server took, and settle, which waits until check holds
// while each turn lets the record's calls and renders run.
interface Rig {
  container: HTMLElement;
  calls: Calls;
  settle: (check: () => boolean, what: string) => Promise<void>;
  // rerender draws the same record element again with the props given in place of the setup's.
  rerender: (next: Partial<ContractRecordProps>) => Promise<void>;
}

// withRecord draws the record of tide-alerts with the setup given, runs the check on it, and removes the DOM however the
// check ends.
async function withRecord(t: TestContext, setup: Setup, check: (rig: Rig) => Promise<void>): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  const { contracts, calls } = serve(setup.script ?? {});
  const props: ContractRecordProps = {
    id,
    clients: { contracts },
    writer,
    hasEpic: setup.hasEpic ?? false,
    read: new Set(setup.read ?? []),
    onRead: setup.onRead ?? (() => undefined),
    live: setup.live ?? true,
    onAbout: setup.onAbout,
    aboutId: setup.aboutId,
    onChanged: setup.onChanged,
    onTalk: setup.onTalk,
  };
  try {
    await act(() => {
      render(<ContractRecord {...props} />, container);
    });
    await check({
      container,
      calls,
      settle: (done, what) => until(t.signal, done, what, { step: () => act(turn) }),
      rerender: (next) =>
        act(() => {
          render(<ContractRecord {...props} {...next} />, container);
        }),
    });
  } finally {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  }
}

// drawn waits until the record has drawn the contract.
function drawn(rig: Rig): Promise<void> {
  return rig.settle(() => rig.container.querySelector(".dline") !== null, "the contract to be drawn");
}

// all returns the elements under root that match selector, in document order.
function all(root: ParentNode, selector: string): HTMLElement[] {
  return Array.from(root.querySelectorAll<HTMLElement>(selector));
}

// findButton returns the button under root whose text is text, or null when there is none.
function findButton(root: ParentNode, text: string): HTMLButtonElement | null {
  return Array.from(root.querySelectorAll<HTMLButtonElement>("button")).find((b) => b.textContent.trim() === text) ?? null;
}

// button returns the button under root whose text is text, which must be there.
function button(root: ParentNode, text: string): HTMLButtonElement {
  const found = findButton(root, text);
  assert.ok(found !== null, `no button reads ${text}`);
  return found;
}

// textOf returns the text of the first element under root that matches selector, which must be there.
function textOf(root: ParentNode, selector: string): string {
  const found = root.querySelector(selector);
  assert.ok(found !== null, `no element matches ${selector}`);
  return found.textContent.trim();
}

// hasText reports whether a note under root reads exactly text.
function hasText(root: ParentNode, text: string): boolean {
  return all(root, ".inote").some((note) => note.textContent.trim() === text);
}

// alertText is the text of the alert under root, or "" when none is shown.
function alertText(root: ParentNode): string {
  return root.querySelector('[role="alert"]')?.textContent.trim() ?? "";
}

// versionRow returns the row of the versions list whose chip reads chip.
function versionRow(root: ParentNode, chip: string): HTMLElement {
  const found = all(root, ".tbl.versions button.tr").find((row) => row.querySelector(".chip")?.textContent.trim() === chip);
  assert.ok(found !== undefined, `no version row reads ${chip}`);
  return found;
}

// rowOf returns the row of the contract's body that carries the id.
function rowOf(root: ParentNode, rowId: string): HTMLElement {
  const found = root.querySelector<HTMLElement>(`[data-row="${rowId}"]`);
  assert.ok(found !== null, `no row ${rowId}`);
  return found;
}

// talkButton returns the control of a row that talks about it.
function talkButton(row: HTMLElement): HTMLButtonElement {
  const found = row.querySelector<HTMLButtonElement>('button[aria-label="Talk about this row"]');
  assert.ok(found !== null, "the row has no talk control");
  return found;
}

// rowControls counts the controls that talk about a row: each row's control and each addition's Keep and Strike.
function rowControls(root: ParentNode): number {
  return all(root, "button").filter(
    (b) =>
      b.getAttribute("aria-label") === "Talk about this row" ||
      b.textContent.trim() === "Keep" ||
      b.textContent.trim() === "Strike",
  ).length;
}

// press clicks the element inside act, so the renders the click makes are applied.
async function press(element: HTMLElement): Promise<void> {
  await act(() => {
    element.click();
  });
}

// type writes text into the set-aside field, as a viewer's typing does.
async function type(root: ParentNode, text: string): Promise<void> {
  const field = root.querySelector<HTMLInputElement>('input[aria-label="Why you set it aside"]');
  assert.ok(field !== null, "no set-aside field is open");
  await act(() => {
    field.value = text;
    field.dispatchEvent(new Event("input"));
  });
}

// withNewerVersion is the record with a third version written after the two it holds, and the contract at version 3.
function withNewerVersion(get: ContractsGetResponse): ContractsGetResponse {
  const contract = get.contract;
  const last = get.versions.at(-1);
  assert.ok(contract !== undefined && last !== undefined, "the record has a contract and a version");
  return create(ContractsGetResponseSchema, {
    contract: create(ContractSchema, { ...contract, version: 3n }),
    versions: [
      ...get.versions,
      create(ContractVersionSchema, { ...last, version: 3n, time: at("@14:50"), words: "A third version." }),
    ],
    states: get.states,
  });
}

// olderAnswered is the record with its first version's decision answered, so only the version being old keeps Accept
// off, and not an open decision.
function olderAnswered(get: ContractsGetResponse): ContractsGetResponse {
  const first = get.versions[0];
  const decision = first.decisions[0];
  return create(ContractsGetResponseSchema, {
    contract: get.contract,
    versions: [
      create(ContractVersionSchema, {
        ...first,
        decisions: [create(ContractDecisionSchema, { ...decision, answer: "The next two days only" })],
      }),
      ...get.versions.slice(1),
    ],
    states: get.states,
  });
}

// gate is a promise a test opens to let a held call answer. It is the test's own signal, never a timer.
function gate(): { wait: Promise<void>; open: () => void } {
  let open: () => void = () => undefined;
  const wait = new Promise<void>((resolve) => {
    open = () => resolve();
  });
  return { wait, open };
}

// FakeIntersectionObserver is the observer a test installs on globalThis. It keeps each instance, what it observes, and
// whether it was disconnected, and the test reports an entry to it.
class FakeIntersectionObserver {
  static made: FakeIntersectionObserver[] = [];
  readonly observed: Element[] = [];
  disconnected = false;
  constructor(readonly callback: IntersectionObserverCallback) {
    FakeIntersectionObserver.made.push(this);
  }
  observe(target: Element): void {
    this.observed.push(target);
  }
  disconnect(): void {
    this.disconnected = true;
  }
}

// withObserver installs the fake observer for the check, and removes it however the check ends.
async function withObserver(check: () => Promise<void>): Promise<void> {
  FakeIntersectionObserver.made = [];
  Object.assign(globalThis, { IntersectionObserver: FakeIntersectionObserver });
  try {
    await check();
  } finally {
    Reflect.deleteProperty(globalThis, "IntersectionObserver");
  }
}

test("ContractActions: Accept is disabled while a decision is open, even once its section is read", async (t) => {
  await withRecord(t, { script: { get: () => tideProposed }, read: [key2] }, async (rig) => {
    await drawn(rig);
    assert.equal(button(rig.container, "Accept").disabled, true);
  });
});

test("ContractActions: at severe care Accept waits until the irreversible section is shown", async (t) => {
  // No IntersectionObserver is installed here, so the section is never shown and Accept stays off.
  await withRecord(t, { script: { get: () => tideReady } }, async (rig) => {
    await drawn(rig);
    assert.equal(textOf(rig.container, ".cmdbar .inote"), "1 thing left: read “can’t be undone” before Accept.");
    assert.equal(button(rig.container, "Accept").disabled, true);
  });
  await withRecord(t, { script: { get: () => tideReady }, read: [key2] }, async (rig) => {
    await drawn(rig);
    assert.equal(textOf(rig.container, ".cmdbar .inote"), "Nothing is left to settle.");
    assert.equal(button(rig.container, "Accept").disabled, false);
  });
});

test("ContractActions: the irreversible section is marked read once it is on screen", async (t) => {
  const reads: string[] = [];
  await withObserver(() =>
    withRecord(t, { script: { get: () => tideReady }, onRead: (key) => reads.push(key) }, async (rig) => {
      await drawn(rig);
      const [observer] = FakeIntersectionObserver.made;
      assert.ok(observer !== undefined, "the irreversible section is observed");
      const [observed] = observer.observed;
      assert.equal(observed.getAttribute("aria-label"), irreversible);
      observer.callback([{ isIntersecting: true } as IntersectionObserverEntry], observer as unknown as IntersectionObserver);
      assert.deepEqual(reads, [key2]);
      assert.equal(observer.disconnected, true);
    }),
  );
});

test("ContractActions: a press re-reads the contract and sends one Accept with empty words", async (t) => {
  let changed = 0;
  await withRecord(t, { script: { get: () => tideReady }, read: [key2], onChanged: () => (changed += 1) }, async (rig) => {
    await drawn(rig);
    await press(button(rig.container, "Accept"));
    await rig.settle(() => rig.calls.get === 3 && changed === 1, "the Accept to be sent and the contract drawn again");
    assert.deepEqual(rig.calls.accept, [""]);
    assert.deepEqual(rig.calls.setAside, []);
    assert.deepEqual(rig.calls.reopen, []);
  });
});

test("ContractActions: a press that finds a newer version sends nothing, draws it, and says so", async (t) => {
  const newer = withNewerVersion(tideReady);
  await withRecord(t, { script: { get: (call) => (call === 1 ? tideReady : newer) }, read: [key2] }, async (rig) => {
    await drawn(rig);
    await press(button(rig.container, "Accept"));
    await rig.settle(() => hasText(rig.container, newerNote), "the newer version to be drawn");
    assert.deepEqual(rig.calls.accept, []);
    assert.equal(
      textOf(rig.container, ".dline .ink-3"),
      `v3 proposed ${clock(at("@14:50"))} · first written ${clock(at("@14:15"))}`,
    );
    assert.equal(button(rig.container, "Accept").disabled, true);
  });
});

test("ContractActions: while a press is sending, Accept and Set aside are disabled, and each write is sent once", async (t) => {
  const reread = gate();
  const accepted = gate();
  let changed = 0;
  await withRecord(
    t,
    {
      script: {
        get: async (call) => {
          if (call === 2) {
            await reread.wait;
          }
          return tideReady;
        },
        accept: () => accepted.wait,
      },
      read: [key2],
      onChanged: () => (changed += 1),
    },
    async (rig) => {
      await drawn(rig);
      await press(button(rig.container, "Accept"));
      await rig.settle(() => rig.calls.get === 2, "the re-read to start");
      assert.equal(button(rig.container, "Accept").disabled, true);
      assert.equal(button(rig.container, "Set aside…").disabled, true);
      reread.open();
      await rig.settle(() => rig.calls.accept.length === 1, "the Accept to start");
      assert.equal(button(rig.container, "Accept").disabled, true);
      assert.equal(button(rig.container, "Set aside…").disabled, true);
      accepted.open();
      await rig.settle(() => rig.calls.get === 3 && changed === 1, "the Accept to finish");
      assert.deepEqual(rig.calls.accept, [""]);
    },
  );
});

test("ContractActions: Set aside needs words, then sends one SetAside with them and nothing else", async (t) => {
  let changed = 0;
  await withRecord(t, { script: { get: () => tideReady }, read: [key2], onChanged: () => (changed += 1) }, async (rig) => {
    await drawn(rig);
    await press(button(rig.container, "Set aside…"));
    assert.equal(button(rig.container, "Set aside").disabled, true);
    await type(rig.container, "Not this season.");
    assert.equal(button(rig.container, "Set aside").disabled, false);
    await press(button(rig.container, "Set aside"));
    await rig.settle(() => rig.calls.get === 2 && changed === 1, "the set aside to be sent and the contract drawn again");
    assert.deepEqual(rig.calls.setAside, ["Not this season."]);
    assert.deepEqual(rig.calls.accept, []);
  });
});

test("ContractActions: Reopen sends one Reopen with empty words on a set-aside contract", async (t) => {
  let changed = 0;
  await withRecord(t, { script: { get: () => tideSetAside }, onChanged: () => (changed += 1) }, async (rig) => {
    await drawn(rig);
    await press(button(rig.container, "Reopen"));
    await rig.settle(() => rig.calls.get === 2 && changed === 1, "the reopen to be sent and the contract drawn again");
    assert.deepEqual(rig.calls.reopen, [""]);
    assert.deepEqual(rig.calls.accept, []);
  });
});

test("ContractActions: a refused Accept shows its reason and the contract looks as it did", async (t) => {
  let changed = 0;
  const refusal = () => {
    throw new ConnectError("the decision is still open", Code.FailedPrecondition);
  };
  await withRecord(
    t,
    { script: { get: () => tideReady, accept: refusal }, read: [key2], onChanged: () => (changed += 1) },
    async (rig) => {
      await drawn(rig);
      await press(button(rig.container, "Accept"));
      await rig.settle(() => alertText(rig.container) === "the decision is still open", "the refusal to be shown");
      assert.equal(changed, 0);
      assert.equal(button(rig.container, "Accept").disabled, false);
      assert.equal(
        textOf(rig.container, ".dline .ink-3"),
        `v2 proposed ${clock(at("@14:39"))} · first written ${clock(at("@14:15"))}`,
      );
    },
  );
});

test("ContractActions: a failed re-read at the press sends no Accept and shows its reason", async (t) => {
  const reread = (call: number) => {
    if (call === 1) {
      return tideReady;
    }
    throw new ConnectError("orreryd is not answering", Code.Unavailable);
  };
  await withRecord(t, { script: { get: reread }, read: [key2] }, async (rig) => {
    await drawn(rig);
    await press(button(rig.container, "Accept"));
    await rig.settle(() => alertText(rig.container) === "orreryd is not answering", "the re-read's failure to be shown");
    assert.deepEqual(rig.calls.accept, []);
    assert.equal(button(rig.container, "Accept").disabled, false);
  });
});

test("ContractActions: a record given another contract drops the one it drew until the other is read", async (t) => {
  const held = new Promise<never>(() => undefined);
  const gets = (call: number) => (call === 1 ? tideReady : held);
  await withRecord(t, { script: { get: gets }, read: [key2] }, async (rig) => {
    await drawn(rig);
    await rig.rerender({ id: "harbour/shift-export" });
    await rig.settle(() => rig.calls.get === 2, "the other contract to be asked for");
    assertNone(rig.container.querySelector(".dline"));
    assertNone(findButton(rig.container, "Accept"));
    assert.equal(hasText(rig.container, "Reading the contract…"), true);
  });
});

test("ContractActions: a refused Set aside shows its reason and the set-aside words stay for a retry", async (t) => {
  let changed = 0;
  const refusal = () => {
    throw new ConnectError("the contract changed", Code.Aborted);
  };
  await withRecord(
    t,
    { script: { get: () => tideReady, setAside: refusal }, read: [key2], onChanged: () => (changed += 1) },
    async (rig) => {
      await drawn(rig);
      await press(button(rig.container, "Set aside…"));
      await type(rig.container, "Not this season.");
      await press(button(rig.container, "Set aside"));
      await rig.settle(() => alertText(rig.container) === "the contract changed", "the refusal to be shown");
      assert.equal(changed, 0);
      assert.equal(button(rig.container, "Set aside").disabled, false);
      assert.equal(rig.container.querySelector<HTMLInputElement>("input")?.value, "Not this season.");
    },
  );
});

test("ContractActions: a refused Reopen shows its reason and the contract stays set aside", async (t) => {
  let changed = 0;
  const refusal = () => {
    throw new ConnectError("the contract was accepted", Code.FailedPrecondition);
  };
  await withRecord(
    t,
    { script: { get: () => tideSetAside, reopen: refusal }, onChanged: () => (changed += 1) },
    async (rig) => {
      await drawn(rig);
      await press(button(rig.container, "Reopen"));
      await rig.settle(() => alertText(rig.container) === "the contract was accepted", "the refusal to be shown");
      assert.equal(changed, 0);
      assert.equal(button(rig.container, "Reopen").disabled, false);
      assert.equal(
        textOf(rig.container, ".dline .ink-3"),
        `v2 set aside by you ${clock(at("@14:40"))} with “Not until the pilots’ season ends.”`,
      );
    },
  );
});

test("ContractActions: a refused read shows contract unavailable with the reason", async (t) => {
  const refusal = () => {
    throw new ConnectError("no such contract", Code.NotFound);
  };
  await withRecord(t, { script: { get: refusal } }, async (rig) => {
    await rig.settle(() => alertText(rig.container) !== "", "the refusal to be shown");
    assert.equal(alertText(rig.container), "contract unavailable no such contract");
    assertNone(rig.container.querySelector(".dline"));
  });
});

test("ContractActions: Talk about it is drawn only when the page gives onTalk, and presses it", async (t) => {
  let talked = 0;
  await withRecord(t, { script: { get: () => tideReady } }, async (rig) => {
    await drawn(rig);
    assertNone(findButton(rig.container, "Talk about it"));
  });
  await withRecord(t, { script: { get: () => tideReady }, onTalk: () => (talked += 1) }, async (rig) => {
    await drawn(rig);
    await press(button(rig.container, "Talk about it"));
    assert.equal(talked, 1);
  });
});

test("ContractActions: the accepted and set-aside record lines name who did it, when, and with what words", async (t) => {
  await withRecord(t, { script: { get: () => tideAccepted }, read: [key2] }, async (rig) => {
    await drawn(rig);
    assert.equal(
      textOf(rig.container, ".dline .ink-3"),
      `v2 accepted by you ${clock(at("@14:40"))} with “Accept, with the next two days only.”`,
    );
  });
  await withRecord(t, { script: { get: () => tideSetAside } }, async (rig) => {
    await drawn(rig);
    assert.equal(
      textOf(rig.container, ".dline .ink-3"),
      `v2 set aside by you ${clock(at("@14:40"))} with “Not until the pilots’ season ends.”`,
    );
  });
});

test("ContractActions: an accepted contract with no epic offers Set aside, and one with an epic does not", async (t) => {
  await withRecord(t, { script: { get: () => tideAccepted }, hasEpic: false }, async (rig) => {
    await drawn(rig);
    assert.ok(findButton(rig.container, "Set aside…") !== null);
    assertNone(findButton(rig.container, "Accept"));
  });
  await withRecord(t, { script: { get: () => tideAccepted }, hasEpic: true }, async (rig) => {
    await drawn(rig);
    assertNone(findButton(rig.container, "Set aside…"));
  });
});

test("ContractVersions: the versions table names its columns: the version, when, by and what changed", async (t) => {
  await withRecord(t, { script: { get: () => tideProposed } }, async (rig) => {
    await drawn(rig);
    await press(button(rig.container, "2 versions"));
    const head = rig.container.querySelector(".tbl.versions .tr.head");
    assert.deepEqual(
      [...(head?.querySelectorAll("span") ?? [])].map((cell) => cell.textContent),
      ["", "when", "by", "what changed"],
    );
  });
});

test("ContractVersions: the versions list shows the newest first, each with its time, writer and what changed", async (t) => {
  await withRecord(t, { script: { get: () => tideProposed } }, async (rig) => {
    await drawn(rig);
    await press(button(rig.container, "2 versions"));
    const rows = all(rig.container, ".tbl.versions button.tr");
    assert.deepEqual(
      rows.map((row) => textOf(row, ".chip")),
      ["v2", "v1"],
    );
    assert.deepEqual(
      rows.map((row) => textOf(row, ".id")),
      [clock(at("@14:39")), clock(at("@14:15"))],
    );
    assert.ok(rows[0].textContent.includes(writer("sess-tide-16")));
    assert.deepEqual(
      all(rows[0], ".idc").map((chip) => chip.textContent.trim()),
      ["F2", "R3"],
    );
    assert.ok(rows[0].textContent.includes("one alert per table, where v1 said one per table and day."));
  });
});

test("ContractVersions: an older version is read-only, says it is not the newest, and reads the newest back", async (t) => {
  await withRecord(t, { script: { get: () => tideProposed }, read: [key1, key2] }, async (rig) => {
    await drawn(rig);
    await press(button(rig.container, "2 versions"));
    await press(versionRow(rig.container, "v1"));
    assert.equal(textOf(rig.container, ".banner span"), "v1 of 2 · not the newest");
    assert.ok(rowOf(rig.container, "F2").textContent.includes("reduce: one alert per table and day."));
    assertNone(findButton(rig.container, "Set aside…"));
    await press(button(rig.container, "Read the newest"));
    assertNone(rig.container.querySelector(".banner"));
    assert.ok(rowOf(rig.container, "F2").textContent.includes("reduce: one alert per table; repeats are dropped."));
  });
});

test("ContractVersions: an older version is not Accept-able even when no decision is open", async (t) => {
  const script = { get: (call: number) => (call === 1 ? olderAnswered(tideReady) : tideReady) };
  await withRecord(t, { script, read: [key1, key2] }, async (rig) => {
    await drawn(rig);
    await press(button(rig.container, "2 versions"));
    await press(versionRow(rig.container, "v1"));
    assert.equal(button(rig.container, "Accept").disabled, true);
    await press(button(rig.container, "Read the newest"));
    assert.equal(button(rig.container, "Accept").disabled, false);
  });
});

test("ContractVersions: a contract with one version reads 1 version and lists only that one", async (t) => {
  const single = create(ContractsGetResponseSchema, {
    contract: tideProposed.contract,
    versions: tideProposed.versions.slice(0, 1),
    states: tideProposed.states,
  });
  await withRecord(t, { script: { get: () => single } }, async (rig) => {
    await drawn(rig);
    await press(button(rig.container, "1 version"));
    assert.equal(all(rig.container, ".tbl.versions button.tr").length, 1);
  });
});

test("ContractMine: row controls are drawn while live on the newest proposed version, each sending its row", async (t) => {
  const abouts: RowAbout[] = [];
  await withRecord(t, { script: { get: () => tideProposed }, onAbout: (about) => abouts.push(about) }, async (rig) => {
    await drawn(rig);
    await press(talkButton(rowOf(rig.container, "F2")));
    await press(button(rig.container, "Keep"));
    assert.deepEqual(abouts, [
      { id: "F2", text: "The office received the same alert many times." },
      { id: "alerts/send.go", text: "Keep: Retry a failed send three times." },
    ]);
  });
});

test("ContractMine: the rows an open decision concerns are marked as mine", async (t) => {
  await withRecord(t, { script: { get: () => tideProposed } }, async (rig) => {
    await drawn(rig);
    assert.equal(rowOf(rig.container, "R1").classList.contains("attention"), true);
    assert.equal(rowOf(rig.container, "R2").classList.contains("attention"), false);
  });
});

test("ContractMine: no row control is drawn once the conversation has ended, on an older version, or accepted", async (t) => {
  const onAbout = () => undefined;
  await withRecord(t, { script: { get: () => tideProposed }, live: false, onAbout }, async (rig) => {
    await drawn(rig);
    assert.equal(rowControls(rig.container), 0);
  });
  await withRecord(t, { script: { get: () => tideProposed }, live: true, onAbout }, async (rig) => {
    await drawn(rig);
    await press(button(rig.container, "2 versions"));
    await press(versionRow(rig.container, "v1"));
    assert.equal(rowControls(rig.container), 0);
  });
  await withRecord(t, { script: { get: () => tideAccepted }, live: true, onAbout }, async (rig) => {
    await drawn(rig);
    assert.equal(rowControls(rig.container), 0);
  });
});

test("ContractMine: a proposed contract whose conversation has ended says so, and a set-aside one still offers Reopen", async (t) => {
  await withRecord(t, { script: { get: () => tideProposed }, live: false }, async (rig) => {
    await drawn(rig);
    assert.equal(hasText(rig.container, noLive), true);
  });
  await withRecord(t, { script: { get: () => tideProposed }, live: true }, async (rig) => {
    await drawn(rig);
    assert.equal(hasText(rig.container, noLive), false);
  });
  await withRecord(t, { script: { get: () => tideSetAside }, live: false }, async (rig) => {
    await drawn(rig);
    assert.equal(hasText(rig.container, noLive), false);
    assert.ok(findButton(rig.container, "Reopen") !== null);
  });
});

test("ContractMine: a set-aside contract with a decision open draws no attention and says Decisions needed: none", async (t) => {
  await withRecord(t, { script: { get: () => tideSetAside } }, async (rig) => {
    await drawn(rig);
    assert.equal(rig.container.querySelectorAll(".attention").length, 0);
    assert.equal(rig.container.querySelectorAll(".unread").length, 0);
    assert.equal(textOf(rig.container, ".dline .facts > span:nth-child(2)"), "Decisions needed: none");
  });
});

test("ContractMine: an older version of a proposed contract draws no attention and says Decisions needed: none", async (t) => {
  const withOpen = create(ContractsGetResponseSchema, {
    contract: tideProposed.contract,
    versions: [{ ...tideProposed.versions[0], decisions: tideProposed.versions[1].decisions }, tideProposed.versions[1]],
    states: tideProposed.states,
  });
  await withRecord(t, { script: { get: () => withOpen } }, async (rig) => {
    await drawn(rig);
    await press(button(rig.container, "2 versions"));
    await press(versionRow(rig.container, "v1"));
    assert.equal(rig.container.querySelectorAll(".attention").length, 0);
    assert.equal(textOf(rig.container, ".dline .facts > span:nth-child(2)"), "Decisions needed: none");
  });
});

test("ContractMine: the row control of the About is lit in the record, and no other", async (t) => {
  await withRecord(t, { script: { get: () => tideProposed }, onAbout: () => undefined, aboutId: "F2" }, async (rig) => {
    await drawn(rig);
    assert.deepEqual(
      all(rig.container, ".say.on").map((say) => say.closest(".tr")?.getAttribute("data-row")),
      ["F2"],
    );
  });
});
