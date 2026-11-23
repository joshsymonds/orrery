import assert from "node:assert/strict";
import { type TestContext, test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { Code, createRouterTransport } from "@connectrpc/connect";
import { render } from "preact";
import { act } from "preact/test-utils";

import { AccountsPage } from "./accountspage";
import { couldNotTell } from "./accountswrite";
import { createClients } from "./client";
import {
  type AccountsSeed,
  type AccountsServe,
  accounts as seedAccounts,
  accountsServe,
  registry as seedRegistry,
} from "./fixtures/accounts";
import { RouteRegistrySchema } from "./gen/orrery/v1/accounts_pb";
import { mockTimers } from "./testclock";
import { assertNone, installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until } from "./testwait";

// Page is the accounts page drawn over a fake of the accounts and routes. fake is the fake, container the DOM the page draws
// into, and settle waits until check holds, letting the page draw between checks.
interface Page {
  fake: AccountsServe;
  container: HTMLElement;
  settle(check: () => boolean, what: string): Promise<void>;
}

// withPage mounts the accounts page over a fake seeded with seed, in a fresh DOM with its timers on the fake clock, and runs
// check. before may refuse or drop the fake's calls before the page mounts. The page is unmounted and the DOM removed when
// check returns or throws.
async function withPage(
  t: TestContext,
  seed: AccountsSeed,
  check: (page: Page) => Promise<void>,
  before: (fake: AccountsServe) => void = () => undefined,
): Promise<void> {
  mockTimers(t, ["setTimeout"]);
  const window = installTestDom();
  const fake = accountsServe(seed);
  before(fake);
  const clients = createClients(createRouterTransport((router) => fake.register(router)));
  const container = document.createElement("div");
  document.body.append(container);
  const teardown = teardownOnce(t.signal, async () => {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  });
  const settle = (ready: () => boolean, what: string) => until(t.signal, ready, what, { step: () => act(turn) });
  try {
    await act(() => {
      render(<AccountsPage clients={clients} narrow={false} />, container);
    });
    await settle(
      () => container.querySelector("table") !== null || container.querySelector(".page-note") !== null,
      "the page's first read",
    );
    await check({ fake, container, settle });
  } finally {
    await teardown();
  }
}

// rowOf is the row the table draws for the account with id, which must be drawn.
function rowOf(container: HTMLElement, id: string): HTMLTableRowElement {
  const row = container.querySelector<HTMLTableRowElement>(`tr[data-account="${id}"]`);
  assert.ok(row !== null, `the table draws no row for ${id}`);
  return row;
}

// cellsOf is the text of each cell of an account row, before its actions cell.
function cellsOf(row: HTMLTableRowElement): string[] {
  return [...row.querySelectorAll("td")].slice(0, -1).map((cell) => cell.textContent ?? "");
}

// buttonsOf is the label of each button in an account row.
function buttonsOf(row: HTMLElement): string[] {
  return [...row.querySelectorAll("button")].map((button) => button.textContent ?? "");
}

// buttonIn is the button of row labelled label, which the row must draw.
function buttonIn(row: HTMLElement, label: string): HTMLButtonElement {
  const button = [...row.querySelectorAll("button")].find((candidate) => candidate.textContent === label);
  assert.ok(button !== undefined, `the row draws no ${label} button`);
  return button;
}

// fieldIn is the name field of row, which the row must draw while it is being renamed.
function fieldIn(row: HTMLElement): HTMLInputElement {
  const field = row.querySelector<HTMLInputElement>("input");
  assert.ok(field !== null, "the row draws no name field");
  return field;
}

// noteOf is the text of the note under the account row with id, or null when that row has no note.
function noteOf(container: HTMLElement, id: string): string | null {
  const next = rowOf(container, id).nextElementSibling;
  return next?.classList.contains("acc-note-row") ? (next.textContent ?? "") : null;
}

// press clicks element, and lets Preact draw what the click changed.
async function press(element: HTMLElement): Promise<void> {
  await act(() => {
    element.dispatchEvent(new Event("click", { bubbles: true, cancelable: true }));
  });
}

// typeInto sets the value of a field as a keystroke does, and lets Preact draw the change.
async function typeInto(field: HTMLInputElement, value: string): Promise<void> {
  await act(() => {
    field.value = value;
    field.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

// methodsSince is the method of each call the fake received from its call number start on.
function methodsSince(fake: AccountsServe, start: number): string[] {
  return fake.calls.slice(start).map((call) => call.method);
}

// seed is the fixture's accounts and registry: personal uses max-1, and the other accounts are used by no context.
const seed: AccountsSeed = { accounts: seedAccounts, registry: seedRegistry };

test("AccountsWebTable: the table draws each account's name, plan, #hash, the contexts that use it and its added day, with Rename and Remove on each row", async (t) => {
  await withPage(t, seed, async ({ container }) => {
    const accountsTable = container.querySelector<HTMLElement>('section[aria-label="Accounts"] table');
    assert.ok(accountsTable !== null, "the page draws no accounts table");
    assert.deepEqual(
      [...accountsTable.querySelectorAll("thead th")].map((head) => head.textContent),
      ["name", "plan", "token", "used by", "added", ""],
    );
    assert.deepEqual(cellsOf(rowOf(container, "max-1")), ["max-1", "Claude Max", "#3f9a1c", "personal", "Nov 12"]);
    assert.deepEqual(cellsOf(rowOf(container, "max-2")), ["max-2", "Claude Max", "#b0217e", "none", "Nov 13"]);
    assert.deepEqual(cellsOf(rowOf(container, "max-3")), ["max-3", "Claude Max", "#5d0c44", "none", "Nov 14"]);
    assert.deepEqual(cellsOf(rowOf(container, "pro-1")), ["pro-1", "Claude Pro", "#e81f05", "none", "Nov 15"]);
    for (const id of ["max-1", "max-2", "max-3", "pro-1"]) {
      assert.deepEqual(buttonsOf(rowOf(container, id)), ["Rename", "Remove"], id);
    }
  });
});

test("AccountsWebTable: the used-by cell names every context that uses the account, joined by commas, from the registry Routes.Get answers", async (t) => {
  const registry = create(RouteRegistrySchema, {
    defaultContext: "personal",
    contexts: [
      { id: "personal", defaultSeat: "anthropic", account: "max-1" },
      { id: "tiltyard", defaultSeat: "anthropic", account: "max-1" },
      { id: "work", defaultSeat: "work-bedrock", account: "" },
    ],
  });
  await withPage(t, { accounts: seedAccounts, registry }, async ({ container }) => {
    assert.equal(cellsOf(rowOf(container, "max-1"))[3], "personal, tiltyard");
    assert.equal(cellsOf(rowOf(container, "max-2"))[3], "none");
  });
});

test("AccountsWebTable: a rename saved shows the name the answer returns, and closes the field", async (t) => {
  await withPage(t, seed, async ({ container, fake, settle }) => {
    const before = fake.calls.length;
    await press(buttonIn(rowOf(container, "max-2"), "Rename"));
    await typeInto(fieldIn(rowOf(container, "max-2")), "team-max");
    await press(buttonIn(rowOf(container, "max-2"), "Save"));
    await settle(() => cellsOf(rowOf(container, "max-2"))[0] === "team-max", "the renamed row");
    assert.deepEqual(methodsSince(fake, before), ["Accounts.Rename"]);
    assertNone(rowOf(container, "max-2").querySelector("input"));
    assert.equal(noteOf(container, "max-2"), null);
  });
});

test("AccountsWebTable: a refused rename keeps the name as it stood and shows the reason with Nothing changed, after a re-read", async (t) => {
  await withPage(t, seed, async ({ container, fake, settle }) => {
    fake.refuseNext("Accounts.Rename", Code.InvalidArgument, "name: taken");
    const before = fake.calls.length;
    await press(buttonIn(rowOf(container, "max-2"), "Rename"));
    await typeInto(fieldIn(rowOf(container, "max-2")), "team-max");
    await press(buttonIn(rowOf(container, "max-2"), "Save"));
    await settle(() => noteOf(container, "max-2") !== null, "the refusal's note");
    assert.equal(cellsOf(rowOf(container, "max-2"))[0], "max-2");
    assert.equal(noteOf(container, "max-2"), "Not renamed: name: taken Nothing changed.");
    assert.deepEqual(methodsSince(fake, before).sort(), ["Accounts.List", "Accounts.Rename", "Routes.Get"]);
  });
});

test("AccountsWebTable: a rename with no answer re-reads the accounts and routes, draws what stands, and says it could not tell", async (t) => {
  await withPage(t, seed, async ({ container, fake, settle }) => {
    fake.dropNext("Accounts.Rename");
    const before = fake.calls.length;
    await press(buttonIn(rowOf(container, "max-2"), "Rename"));
    await typeInto(fieldIn(rowOf(container, "max-2")), "team-max");
    await press(buttonIn(rowOf(container, "max-2"), "Save"));
    await settle(() => noteOf(container, "max-2") === couldNotTell, "the could-not-tell note");
    assert.equal(cellsOf(rowOf(container, "max-2"))[0], "max-2");
    assert.deepEqual(methodsSince(fake, before).sort(), ["Accounts.List", "Accounts.Rename", "Routes.Get"]);
  });
});

test("AccountsWebTable: Cancel closes the rename field, keeps the name, and sends nothing", async (t) => {
  await withPage(t, seed, async ({ container, fake }) => {
    const before = fake.calls.length;
    await press(buttonIn(rowOf(container, "max-2"), "Rename"));
    assert.equal(fieldIn(rowOf(container, "max-2")).value, "max-2");
    await press(buttonIn(rowOf(container, "max-2"), "Cancel"));
    assertNone(rowOf(container, "max-2").querySelector("input"));
    assert.equal(cellsOf(rowOf(container, "max-2"))[0], "max-2");
    assert.deepEqual(methodsSince(fake, before), []);
  });
});

test("AccountsWebTable: a failed read draws its reason with a Retry, and Retry reads both again and draws the table", async (t) => {
  await withPage(
    t,
    seed,
    async ({ container, fake, settle }) => {
      const failure = container.querySelector<HTMLElement>(".page-note");
      assert.ok(failure !== null, "the page draws no failure");
      assert.equal(failure.textContent, "Could not read the accounts and routes: the account store is down Retry");
      assertNone(container.querySelector("table"));
      const before = fake.calls.length;
      await press(buttonIn(failure, "Retry"));
      await settle(() => container.querySelector("table") !== null, "the table after Retry");
      assert.deepEqual(cellsOf(rowOf(container, "max-1")), ["max-1", "Claude Max", "#3f9a1c", "personal", "Nov 12"]);
      assert.deepEqual(methodsSince(fake, before).sort(), ["Accounts.List", "Routes.Get"]);
    },
    (fake) => {
      fake.refuseNext("Accounts.List", Code.Unavailable, "the account store is down");
    },
  );
});
