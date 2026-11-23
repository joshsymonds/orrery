import assert from "node:assert/strict";
import { type TestContext, test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
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

// Page is the accounts page drawn over a fake of the accounts and routes. fake is the fake, clients the clients the page is
// given, container the DOM the page draws into, and settle waits until check holds, letting the page draw between checks.
interface Page {
  fake: AccountsServe;
  clients: ReturnType<typeof createClients>;
  container: HTMLElement;
  settle(check: () => boolean, what: string): Promise<void>;
}

// withPage mounts the accounts page over a fake seeded with seed, in a fresh DOM with its timers on the fake clock, and runs
// check. The page is unmounted and the DOM removed when check returns or throws.
// The page is given the clients as made, or as given changes them.
async function withPage(
  t: TestContext,
  seed: AccountsSeed,
  check: (page: Page) => Promise<void>,
  given: (clients: ReturnType<typeof createClients>) => ReturnType<typeof createClients> = (clients) => clients,
): Promise<void> {
  mockTimers(t, ["setTimeout"]);
  const window = installTestDom();
  const fake = accountsServe(seed);
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
      render(<AccountsPage clients={given(clients)} narrow={false} />, container);
    });
    await settle(
      () => container.querySelector("table") !== null || container.querySelector(".page-note") !== null,
      "the page's first read",
    );
    await check({ fake, clients, container, settle });
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

// idsOf is the id of each account row the table draws, in order.
function idsOf(container: HTMLElement): string[] {
  return [...container.querySelectorAll("tr[data-account]")].map((row) => row.getAttribute("data-account") ?? "");
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

// noteOf is the text of the note under the account row with id, or null when that row has no note.
function noteOf(container: HTMLElement, id: string): string | null {
  const next = rowOf(container, id).nextElementSibling;
  return next?.classList.contains("acc-note-row") ? (next.textContent ?? "") : null;
}

// cellsOf is the text of each cell of an account row, before its actions cell.
function cellsOf(row: HTMLTableRowElement): string[] {
  return [...row.querySelectorAll("td")].slice(0, -1).map((cell) => cell.textContent ?? "");
}

// press clicks element, and lets Preact draw what the click changed.
async function press(element: HTMLElement): Promise<void> {
  await act(() => {
    element.dispatchEvent(new Event("click", { bubbles: true, cancelable: true }));
  });
}

// askRemove presses the Remove of the row with id, which asks in the row, and then presses its confirm, which sends
// Accounts.Remove. Each press is found again in the row, since the ask replaces the buttons.
async function askRemove(container: HTMLElement, id: string): Promise<void> {
  await press(buttonIn(rowOf(container, id), "Remove"));
  await press(buttonIn(rowOf(container, id), "Remove"));
}

// methodsSince is the method of each call the fake received from its call number start on.
function methodsSince(fake: AccountsServe, start: number): string[] {
  return fake.calls.slice(start).map((call) => call.method);
}

// seed is the fixture's accounts and registry: personal uses max-1, and the other accounts are used by no context.
const seed: AccountsSeed = { accounts: seedAccounts, registry: seedRegistry };

test("AccountsWebRemove: Remove asks in the row, naming the account, with Keep and Remove, and sends nothing yet", async (t) => {
  await withPage(t, seed, async ({ container, fake }) => {
    const before = fake.calls.length;
    await press(buttonIn(rowOf(container, "max-3"), "Remove"));
    const row = rowOf(container, "max-3");
    assert.equal(row.querySelector(".sure")?.textContent, "Remove max-3?");
    assert.deepEqual(buttonsOf(row), ["Keep", "Remove"]);
    assert.deepEqual(methodsSince(fake, before), []);
  });
});

test("AccountsWebRemove: Keep closes the ask in the row and calls nothing", async (t) => {
  await withPage(t, seed, async ({ container, fake }) => {
    const before = fake.calls.length;
    await press(buttonIn(rowOf(container, "max-3"), "Remove"));
    await press(buttonIn(rowOf(container, "max-3"), "Keep"));
    assert.deepEqual(buttonsOf(rowOf(container, "max-3")), ["Rename", "Remove"]);
    assertNone(rowOf(container, "max-3").querySelector(".sure"));
    assert.deepEqual(methodsSince(fake, before), []);
  });
});

test("AccountsWebRemove: confirming sends Accounts.Remove, and the answered row is gone", async (t) => {
  await withPage(t, seed, async ({ container, fake, settle }) => {
    const before = fake.calls.length;
    await askRemove(container, "max-3");
    await settle(() => container.querySelector('tr[data-account="max-3"]') === null, "the removed row");
    assert.deepEqual(methodsSince(fake, before), ["Accounts.Remove"]);
    assert.deepEqual(idsOf(container), ["max-1", "max-2", "pro-1"]);
  });
});

test("AccountsWebRemove: a removal refused as in use names every context the fresh Routes.Get holds the account for, and the table stays unchanged", async (t) => {
  const registry = create(RouteRegistrySchema, {
    defaultContext: "personal",
    contexts: [
      { id: "personal", defaultSeat: "anthropic", account: "max-1" },
      { id: "tiltyard", defaultSeat: "anthropic", account: "" },
      { id: "lab", defaultSeat: "anthropic", account: "" },
    ],
  });
  await withPage(t, { accounts: seedAccounts, registry }, async ({ container, fake, clients, settle }) => {
    // tiltyard and lab take max-1 after the page has read the registry, so only a fresh Routes.Get names them.
    await clients.routes.setContextAccount({ context: "tiltyard", account: "max-1" });
    await clients.routes.setContextAccount({ context: "lab", account: "max-1" });
    const before = fake.calls.length;
    await askRemove(container, "max-1");
    await settle(() => noteOf(container, "max-1") !== null, "the in-use sentence");
    assert.equal(
      noteOf(container, "max-1"),
      "max-1 is in use by personal, tiltyard and lab. Move them to another account first, then remove it. Nothing changed.",
    );
    assert.deepEqual(methodsSince(fake, before).sort(), ["Accounts.List", "Accounts.Remove", "Routes.Get"]);
    assert.deepEqual(idsOf(container), ["max-1", "max-2", "max-3", "pro-1"]);
    assert.deepEqual(cellsOf(rowOf(container, "max-1")), ["max-1", "Claude Max", "#3f9a1c", "personal, tiltyard, lab", "Nov 12"]);
    assert.deepEqual(buttonsOf(rowOf(container, "max-1")), ["Rename", "Remove"]);
  });
});

test("AccountsWebRemove: a removal refused as in use by two contexts names both, joined by and", async (t) => {
  const registry = create(RouteRegistrySchema, {
    defaultContext: "personal",
    contexts: [
      { id: "personal", defaultSeat: "anthropic", account: "max-1" },
      { id: "tiltyard", defaultSeat: "anthropic", account: "max-1" },
      { id: "work", defaultSeat: "work-bedrock", account: "" },
    ],
  });
  await withPage(t, { accounts: seedAccounts, registry }, async ({ container, settle }) => {
    await askRemove(container, "max-1");
    await settle(() => noteOf(container, "max-1") !== null, "the in-use sentence");
    assert.equal(
      noteOf(container, "max-1"),
      "max-1 is in use by personal and tiltyard. Move them to another account first, then remove it. Nothing changed.",
    );
    assert.deepEqual(idsOf(container), ["max-1", "max-2", "max-3", "pro-1"]);
  });
});

test("AccountsWebRemove: a removal refused for another reason says Not removed with the reason, and Nothing changed", async (t) => {
  await withPage(t, seed, async ({ container, fake, settle }) => {
    fake.refuseNext("Accounts.Remove", Code.InvalidArgument, "account: busy");
    const before = fake.calls.length;
    await askRemove(container, "max-3");
    await settle(() => noteOf(container, "max-3") !== null, "the refusal's note");
    assert.equal(noteOf(container, "max-3"), "Not removed: account: busy Nothing changed.");
    assert.deepEqual(idsOf(container), ["max-1", "max-2", "max-3", "pro-1"]);
    assert.deepEqual(methodsSince(fake, before).sort(), ["Accounts.List", "Accounts.Remove", "Routes.Get"]);
  });
});

test("AccountsWebRemove: a refusal as in use that the fresh registry no longer backs says Not removed with the reason", async (t) => {
  await withPage(t, seed, async ({ container, fake, settle }) => {
    fake.refuseNext("Accounts.Remove", Code.FailedPrecondition, "account: in use by context personal");
    await askRemove(container, "max-3");
    await settle(() => noteOf(container, "max-3") !== null, "the refusal's note");
    assert.equal(noteOf(container, "max-3"), "Not removed: account: in use by context personal Nothing changed.");
    assert.deepEqual(idsOf(container), ["max-1", "max-2", "max-3", "pro-1"]);
  });
});

test("AccountsWebRemove: a dropped Remove re-reads the accounts and routes, draws what stands, and says it could not tell", async (t) => {
  await withPage(t, seed, async ({ container, fake, settle }) => {
    fake.dropNext("Accounts.Remove");
    const before = fake.calls.length;
    await askRemove(container, "max-3");
    await settle(() => noteOf(container, "max-3") === couldNotTell, "the could-not-tell note");
    assert.deepEqual(idsOf(container), ["max-1", "max-2", "max-3", "pro-1"]);
    assert.deepEqual(methodsSince(fake, before).sort(), ["Accounts.List", "Accounts.Remove", "Routes.Get"]);
  });
});

test("AccountsWebRemove: a Remove that lands but whose answer is dropped re-reads, draws the row gone, and says it could not tell", async (t) => {
  // landsThenDrops gives the page an Accounts client whose Remove reaches the fake, which removes the account, and then fails
  // as a deadline does, so the page has no answer.
  const landsThenDrops = (clients: ReturnType<typeof createClients>): ReturnType<typeof createClients> => ({
    ...clients,
    accounts: {
      ...clients.accounts,
      remove: async (request) => {
        await clients.accounts.remove(request);
        throw new ConnectError("the answer was lost", Code.DeadlineExceeded);
      },
    },
  });
  await withPage(
    t,
    seed,
    async ({ container, fake, settle }) => {
      const before = fake.calls.length;
      await askRemove(container, "max-3");
      await settle(() => container.querySelector(".acc-gone-note") !== null, "the could-not-tell note");
      assert.equal(container.querySelector(".acc-gone-note")?.textContent, couldNotTell);
      assert.deepEqual(idsOf(container), ["max-1", "max-2", "pro-1"]);
      assert.deepEqual(methodsSince(fake, before).sort(), ["Accounts.List", "Accounts.Remove", "Routes.Get"]);
    },
    landsThenDrops,
  );
});
