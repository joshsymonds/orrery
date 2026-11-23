import assert from "node:assert/strict";
import { type TestContext, test } from "node:test";

import { clone, toJson } from "@bufbuild/protobuf";
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
import { type RouteContext, type RouteRegistry, RouteRegistrySchema } from "./gen/orrery/v1/accounts_pb";
import { mockTimers } from "./testclock";
import { assertNone, installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until } from "./testwait";

type Clients = ReturnType<typeof createClients>;

// Page is the accounts page drawn over a fake of the accounts and routes. fake is the fake, clients the clients it is reached
// through, container the DOM the page draws into, and settle waits until check holds, letting the page draw between checks.
interface Page {
  fake: AccountsServe;
  clients: Clients;
  container: HTMLElement;
  settle(check: () => boolean, what: string): Promise<void>;
}

// withPage mounts the accounts page over a fake seeded with seed, in a fresh DOM with its timers on the fake clock, and runs
// check. The page is unmounted and the DOM removed when check returns or throws.
async function withPage(t: TestContext, seed: AccountsSeed, check: (page: Page) => Promise<void>): Promise<void> {
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
      render(<AccountsPage clients={clients} narrow={false} />, container);
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

// cardOf is the context card the section draws for the context with id, which must be drawn.
function cardOf(container: HTMLElement, id: string): HTMLElement {
  const card = container.querySelector<HTMLElement>(`article[data-context="${id}"]`);
  assert.ok(card !== null, `the section draws no card for ${id}`);
  return card;
}

// buttonIn is the button the card draws with the text label, which the card must draw.
function buttonIn(card: HTMLElement, label: string): HTMLButtonElement {
  const button = [...card.querySelectorAll<HTMLButtonElement>("button")].find(
    (candidate) => candidate.textContent === label,
  );
  assert.ok(button !== undefined, `the card draws no ${label} button`);
  return button;
}

// editOpenIn is whether the card draws an edit table, which is open while Edit has been pressed and no Save or Cancel has
// closed it.
function editOpenIn(container: HTMLElement, id: string): boolean {
  return cardOf(container, id).querySelector(".ctx-edit") !== null;
}

// editRowsOf is the model selector of each row of the card's edit table, in order.
function editRowsOf(card: HTMLElement): string[] {
  return [...card.querySelectorAll("table.ctx-edit tbody tr")].map((row) => row.getAttribute("data-selector") ?? "");
}

// seatSelectOf is the seat select of the edit row for selector, which the card must draw.
function seatSelectOf(card: HTMLElement, selector: string): HTMLSelectElement {
  const select = card.querySelector<HTMLSelectElement>(`tr[data-selector="${selector}"] select`);
  assert.ok(select !== null, `the edit draws no seat select for ${selector}`);
  return select;
}

// seatsOf is the value of each option of a seat select, in order.
function seatsOf(select: HTMLSelectElement): string[] {
  return [...select.options].map((option) => option.value);
}

// noteIn is the text of the note on the card for the context with id, or null when that card has none.
function noteIn(container: HTMLElement, id: string): string | null {
  const note = cardOf(container, id).querySelector(".acc-note");
  return note === null ? null : (note.textContent ?? "");
}

// press clicks button, and lets Preact draw what the click changed.
async function press(button: HTMLButtonElement): Promise<void> {
  await act(() => {
    button.click();
  });
}

// pick chooses seat in select, as a choice does, and lets Preact draw what the choice changed.
async function pick(select: HTMLSelectElement, seat: string): Promise<void> {
  await act(() => {
    select.value = seat;
    select.dispatchEvent(new Event("change", { bubbles: true }));
  });
}

// methodsSince is the method of each call the fake received from its call number start on.
function methodsSince(fake: AccountsServe, start: number): string[] {
  return fake.calls.slice(start).map((call) => call.method);
}

// replaceSince is the registry of the first Routes.Replace the fake received from its call number start on, which must exist.
function replaceSince(fake: AccountsServe, start: number): RouteRegistry {
  const [call] = fake.calls.slice(start).filter((candidate) => candidate.method === "Routes.Replace");
  assert.ok(call?.method === "Routes.Replace", "the fake received no Routes.Replace");
  const sent = call.request.registry;
  assert.ok(sent !== undefined, "the Routes.Replace carried no registry");
  return sent;
}

// assertRegistry checks that sent is expected, field for field, and names the difference when they differ.
function assertRegistry(sent: RouteRegistry, expected: RouteRegistry): void {
  assert.deepEqual(toJson(RouteRegistrySchema, sent), toJson(RouteRegistrySchema, expected));
}

// copyOf is a copy of the fixture's registry, which change then edits.
function copyOf(change: (registry: RouteRegistry) => void): RouteRegistry {
  const copy = clone(RouteRegistrySchema, seedRegistry);
  change(copy);
  return copy;
}

// contextIn is the context with id in registry, which must hold it.
function contextIn(registry: RouteRegistry, id: string): RouteContext {
  const context = registry.contexts.find((candidate) => candidate.id === id);
  assert.ok(context !== undefined, `the registry holds no context ${id}`);
  return context;
}

// seed is the fixture's accounts and registry: personal uses max-1 and has two model selectors, example-haiku and example-luna,
// and work has none and uses no account.
const seed: AccountsSeed = { accounts: seedAccounts, registry: seedRegistry };

// fixtureSeats are the seats of the fixture's registry, in the order the registry holds them, which the edit's selects offer.
const fixtureSeats = ["anthropic", "anthropic-haiku", "work-bedrock", "chatgpt-luna", "openrouter-sol"];

test("RoutesWebEdit: Edit opens one row per model selector the context has, in its order, each with a seat select of every seat, and Save changes only the seat picked", async (t) => {
  await withPage(t, seed, async ({ container, fake, settle }) => {
    assertNone(cardOf(container, "personal").querySelector(".ctx-edit"));
    await press(buttonIn(cardOf(container, "personal"), "Edit"));
    const edited = cardOf(container, "personal");
    assert.deepEqual(editRowsOf(edited), ["example-haiku", "example-luna"]);
    assert.deepEqual(seatsOf(seatSelectOf(edited, "example-haiku")), fixtureSeats);
    assert.deepEqual(seatsOf(seatSelectOf(edited, "example-luna")), fixtureSeats);
    assert.equal(seatSelectOf(edited, "example-haiku").value, "anthropic-haiku");
    assert.equal(seatSelectOf(edited, "example-luna").value, "chatgpt-luna");

    const before = fake.calls.length;
    await pick(seatSelectOf(edited, "example-luna"), "openrouter-sol");
    await press(buttonIn(edited, "Save"));
    await settle(() => !editOpenIn(container, "personal"), "the edit closed on the answer");
    assert.deepEqual(methodsSince(fake, before), ["Routes.Get", "Routes.Replace"]);
    assertRegistry(
      replaceSince(fake, before),
      copyOf((next) => {
        contextIn(next, "personal").models["example-luna"] = "openrouter-sol";
      }),
    );
  });
});

test("RoutesWebEdit: Save reads Routes.Get afresh, so another context's account changed since the page loaded is carried in the Replace, with only this context's seat change", async (t) => {
  await withPage(t, seed, async ({ container, clients, fake, settle }) => {
    // Another writer sets work's account after the page has read the routes, so the page still draws work with no account.
    await clients.routes.setContextAccount({ context: "work", account: "max-3" });
    assert.equal(cardOf(container, "work").querySelector<HTMLSelectElement>("select")?.value, "");

    await press(buttonIn(cardOf(container, "personal"), "Edit"));
    await pick(seatSelectOf(cardOf(container, "personal"), "example-haiku"), "chatgpt-luna");
    const before = fake.calls.length;
    await press(buttonIn(cardOf(container, "personal"), "Save"));
    await settle(() => !editOpenIn(container, "personal"), "the edit closed on the answer");
    assert.deepEqual(methodsSince(fake, before), ["Routes.Get", "Routes.Replace"]);
    assertRegistry(
      replaceSince(fake, before),
      copyOf((next) => {
        contextIn(next, "work").account = "max-3";
        contextIn(next, "personal").models["example-haiku"] = "chatgpt-luna";
      }),
    );
    assert.equal(cardOf(container, "work").querySelector<HTMLSelectElement>("select")?.value, "max-3");
  });
});

test("RoutesWebEdit: a refused Replace shows Not saved, its reason and Nothing changed, and the card's bindings stand as they were", async (t) => {
  await withPage(t, seed, async ({ container, fake, settle }) => {
    fake.refuseNext("Routes.Replace", Code.FailedPrecondition, "context personal: chatgpt-luna cannot carry its account");
    const before = fake.calls.length;
    await press(buttonIn(cardOf(container, "personal"), "Edit"));
    await pick(seatSelectOf(cardOf(container, "personal"), "example-luna"), "openrouter-sol");
    await press(buttonIn(cardOf(container, "personal"), "Save"));
    await settle(() => noteIn(container, "personal") !== null, "the refusal's note");
    assert.equal(
      noteIn(container, "personal"),
      "Not saved: context personal: chatgpt-luna cannot carry its account Nothing changed.",
    );
    assert.deepEqual(methodsSince(fake, before), ["Routes.Get", "Routes.Replace"]);

    // Cancel, then Edit again: the rows show the seats the card's bindings stood on before the refused save.
    await press(buttonIn(cardOf(container, "personal"), "Cancel"));
    await press(buttonIn(cardOf(container, "personal"), "Edit"));
    const reopened = cardOf(container, "personal");
    assert.equal(seatSelectOf(reopened, "example-haiku").value, "anthropic-haiku");
    assert.equal(seatSelectOf(reopened, "example-luna").value, "chatgpt-luna");
  });
});

test("RoutesWebEdit: a fresh read that is refused sends no Replace, and says why nothing changed", async (t) => {
  await withPage(t, seed, async ({ container, fake, settle }) => {
    fake.refuseNext("Routes.Get", Code.NotFound, "routes: no registry is stored");
    const before = fake.calls.length;
    await press(buttonIn(cardOf(container, "personal"), "Edit"));
    await pick(seatSelectOf(cardOf(container, "personal"), "example-luna"), "openrouter-sol");
    await press(buttonIn(cardOf(container, "personal"), "Save"));
    await settle(() => noteIn(container, "personal") !== null, "the read's note");
    assert.equal(noteIn(container, "personal"), "Not saved: routes: no registry is stored Nothing changed.");
    assert.deepEqual(methodsSince(fake, before), ["Routes.Get"]);
  });
});

test("RoutesWebEdit: a fresh read with no answer sends no Replace, and says the routes could not be read, with Nothing changed", async (t) => {
  await withPage(t, seed, async ({ container, fake, settle }) => {
    fake.dropNext("Routes.Get");
    const before = fake.calls.length;
    await press(buttonIn(cardOf(container, "personal"), "Edit"));
    await pick(seatSelectOf(cardOf(container, "personal"), "example-luna"), "openrouter-sol");
    await press(buttonIn(cardOf(container, "personal"), "Save"));
    await settle(() => noteIn(container, "personal") !== null, "the read's note");
    assert.equal(noteIn(container, "personal"), "Not saved: the routes could not be read. Nothing changed.");
    assert.deepEqual(methodsSince(fake, before), ["Routes.Get"]);
  });
});

test("RoutesWebEdit: a context the fresh routes no longer hold sends no Replace, and says it was not saved", async (t) => {
  await withPage(t, seed, async ({ container, clients, fake, settle }) => {
    // Another writer replaces the routes without personal after the page has read them, so the page still draws personal.
    const without = clone(RouteRegistrySchema, seedRegistry);
    without.contexts = without.contexts.filter((context) => context.id !== "personal");
    await clients.routes.replace({ registry: without });

    const before = fake.calls.length;
    await press(buttonIn(cardOf(container, "personal"), "Edit"));
    await pick(seatSelectOf(cardOf(container, "personal"), "example-luna"), "openrouter-sol");
    await press(buttonIn(cardOf(container, "personal"), "Save"));
    await settle(() => noteIn(container, "personal") !== null, "the context's note");
    assert.equal(noteIn(container, "personal"), "Not saved: personal is no longer in the routes. Nothing changed.");
    assert.deepEqual(methodsSince(fake, before), ["Routes.Get"]);
  });
});

test("RoutesWebEdit: a Replace with no answer closes the edit, re-reads the accounts and routes, and says it could not tell", async (t) => {
  await withPage(t, seed, async ({ container, fake, settle }) => {
    fake.dropNext("Routes.Replace");
    const before = fake.calls.length;
    await press(buttonIn(cardOf(container, "personal"), "Edit"));
    await pick(seatSelectOf(cardOf(container, "personal"), "example-luna"), "openrouter-sol");
    await press(buttonIn(cardOf(container, "personal"), "Save"));
    await settle(() => noteIn(container, "personal") === couldNotTell, "the could-not-tell note");
    assert.equal(editOpenIn(container, "personal"), false);
    assert.deepEqual(methodsSince(fake, before).sort(), [
      "Accounts.List",
      "Routes.Get",
      "Routes.Get",
      "Routes.Replace",
    ]);
  });
});

test("RoutesWebEdit: Cancel closes the edit with nothing sent, and the card's seats stay as they were", async (t) => {
  await withPage(t, seed, async ({ container, fake }) => {
    const before = fake.calls.length;
    await press(buttonIn(cardOf(container, "personal"), "Edit"));
    await pick(seatSelectOf(cardOf(container, "personal"), "example-luna"), "openrouter-sol");
    await press(buttonIn(cardOf(container, "personal"), "Cancel"));
    assert.equal(editOpenIn(container, "personal"), false);
    assert.deepEqual(methodsSince(fake, before), []);

    await press(buttonIn(cardOf(container, "personal"), "Edit"));
    assert.equal(seatSelectOf(cardOf(container, "personal"), "example-luna").value, "chatgpt-luna");
  });
});
