import assert from "node:assert/strict";
import { type TestContext, test } from "node:test";

import { clone, create } from "@bufbuild/protobuf";
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
import { AccountSchema, RouteRegistrySchema } from "./gen/orrery/v1/accounts_pb";
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
    await check({ fake, container, settle });
  } finally {
    await teardown();
  }
}

// rowOf is the row the accounts table draws for the account with id, which must be drawn.
function rowOf(container: HTMLElement, id: string): HTMLTableRowElement {
  const row = container.querySelector<HTMLTableRowElement>(`tr[data-account="${id}"]`);
  assert.ok(row !== null, `the table draws no row for ${id}`);
  return row;
}

// cellsOf is the text of each cell of an account row, before its actions cell. The used-by cell is the fourth.
function cellsOf(row: HTMLTableRowElement): string[] {
  return [...row.querySelectorAll("td")].slice(0, -1).map((cell) => cell.textContent ?? "");
}

// cardOf is the context card the section draws for the context with id, which must be drawn.
function cardOf(container: HTMLElement, id: string): HTMLElement {
  const card = container.querySelector<HTMLElement>(`article[data-context="${id}"]`);
  assert.ok(card !== null, `the section draws no card for ${id}`);
  return card;
}

// selectOf is the account select of the card for the context with id, which the card must draw.
function selectOf(container: HTMLElement, id: string): HTMLSelectElement {
  const select = cardOf(container, id).querySelector<HTMLSelectElement>("select");
  assert.ok(select !== null, `the card for ${id} draws no select`);
  return select;
}

// noteIn is the text of the note on the card for the context with id, or null when that card has none.
function noteIn(container: HTMLElement, id: string): string | null {
  const note = cardOf(container, id).querySelector(".acc-note");
  return note === null ? null : (note.textContent ?? "");
}

// factsOf is the text of each fact the card draws, in order.
function factsOf(card: HTMLElement): string[] {
  return [...card.querySelectorAll(".ctx-facts > span")].map((fact) => fact.textContent ?? "");
}

// choose picks account in select, as a choice does, and lets Preact draw what the choice changed.
async function choose(select: HTMLSelectElement, account: string): Promise<void> {
  await act(() => {
    select.value = account;
    select.dispatchEvent(new Event("change", { bubbles: true }));
  });
}

// methodsSince is the method of each call the fake received from its call number start on.
function methodsSince(fake: AccountsServe, start: number): string[] {
  return fake.calls.slice(start).map((call) => call.method);
}

// seed is the fixture's accounts and registry: personal uses max-1, and the other accounts are used by no context.
const seed: AccountsSeed = { accounts: seedAccounts, registry: seedRegistry };

// threeContexts is a registry of three contexts: personal, the default, uses max-1; work has no account, so its seats send the
// session's own login; and tiltyard uses max-2. Each test that changes one context checks the others.
const threeContexts: AccountsSeed = {
  accounts: seedAccounts,
  registry: create(RouteRegistrySchema, {
    defaultContext: "personal",
    contexts: [
      { id: "personal", defaultSeat: "anthropic", account: "max-1" },
      { id: "work", defaultSeat: "work-bedrock", account: "" },
      { id: "tiltyard", defaultSeat: "anthropic", account: "max-2" },
    ],
  }),
};

test("RoutesWebAccount: a context's select sets that context to an account, and another context's select to the session's own login", async (t) => {
  await withPage(t, threeContexts, async ({ container, settle }) => {
    await choose(selectOf(container, "work"), "max-3");
    await settle(() => cellsOf(rowOf(container, "max-3"))[3] === "work", "work drawn as using max-3");
    assert.equal(selectOf(container, "work").value, "max-3");

    await choose(selectOf(container, "personal"), "");
    await settle(() => cellsOf(rowOf(container, "max-1"))[3] === "none", "personal drawn as using no account");
    const personal = selectOf(container, "personal");
    assert.equal(personal.value, "");
    assert.equal(personal.options[personal.selectedIndex]?.textContent, "the session's own login");
  });
});

test("RoutesWebAccount: a select change sends one Routes.SetContextAccount, naming that context and its account, and no other call", async (t) => {
  await withPage(t, threeContexts, async ({ container, fake, settle }) => {
    const before = fake.calls.length;
    await choose(selectOf(container, "tiltyard"), "max-3");
    await settle(() => cellsOf(rowOf(container, "max-3"))[3] === "tiltyard", "the answer's registry drawn");
    assert.deepEqual(methodsSince(fake, before), ["Routes.SetContextAccount"]);
    const [call] = fake.calls.slice(before);
    assert.ok(call?.method === "Routes.SetContextAccount");
    assert.equal(call.request.context, "tiltyard");
    assert.equal(call.request.account, "max-3");
  });
});

test("RoutesWebAccount: the answer's registry is drawn, and every other context's card is unchanged", async (t) => {
  await withPage(t, threeContexts, async ({ container, settle }) => {
    const others = ["personal", "work"];
    const before = others.map((id) => ({ html: cardOf(container, id).outerHTML, account: selectOf(container, id).value }));
    await choose(selectOf(container, "tiltyard"), "max-3");
    await settle(() => cellsOf(rowOf(container, "max-3"))[3] === "tiltyard", "the answer's registry drawn");
    assert.equal(selectOf(container, "tiltyard").value, "max-3");
    others.forEach((id, index) => {
      assert.equal(cardOf(container, id).outerHTML, before[index].html, `the card for ${id} changed`);
      assert.equal(selectOf(container, id).value, before[index].account, `the select for ${id} changed`);
    });
  });
});

test("RoutesWebAccount: the answer also redraws the accounts table's used-by column from the registry it returns", async (t) => {
  await withPage(t, threeContexts, async ({ container, settle }) => {
    assert.equal(cellsOf(rowOf(container, "max-2"))[3], "tiltyard");
    assert.equal(cellsOf(rowOf(container, "max-3"))[3], "none");
    await choose(selectOf(container, "tiltyard"), "max-3");
    await settle(() => cellsOf(rowOf(container, "max-3"))[3] === "tiltyard", "the answer's registry drawn");
    assert.equal(cellsOf(rowOf(container, "max-2"))[3], "none");
  });
});

test("RoutesWebAccount: a refused set leaves the select on the account it stood on, and shows the reason with Nothing changed", async (t) => {
  await withPage(t, threeContexts, async ({ container, fake, settle }) => {
    fake.refuseNext("Routes.SetContextAccount", Code.FailedPrecondition, "context: its seats cannot carry an account");
    const before = fake.calls.length;
    await choose(selectOf(container, "personal"), "max-2");
    await settle(() => noteIn(container, "personal") !== null, "the refusal's note");
    assert.equal(selectOf(container, "personal").value, "max-1");
    assert.equal(
      noteIn(container, "personal"),
      "Not set: context: its seats cannot carry an account Nothing changed.",
    );
    assert.equal(cellsOf(rowOf(container, "max-1"))[3], "personal");
    assert.deepEqual(methodsSince(fake, before), ["Routes.SetContextAccount"]);
  });
});

test("RoutesWebAccount: a set with no answer re-reads the accounts and routes, draws the account that stands, and says it could not tell", async (t) => {
  await withPage(t, threeContexts, async ({ container, fake, settle }) => {
    fake.dropNext("Routes.SetContextAccount");
    const before = fake.calls.length;
    await choose(selectOf(container, "work"), "max-3");
    await settle(() => noteIn(container, "work") === couldNotTell, "the could-not-tell note");
    assert.equal(selectOf(container, "work").value, "");
    assert.equal(cellsOf(rowOf(container, "max-3"))[3], "none");
    assert.deepEqual(methodsSince(fake, before).sort(), ["Accounts.List", "Routes.Get", "Routes.SetContextAccount"]);
  });
});

test("RoutesWebAccount: the select offers the session's own login, then every account by name, and shows the context's account", async (t) => {
  const renamed = seedAccounts.map((account) => {
    const copy = clone(AccountSchema, account);
    if (copy.id === "max-2") {
      copy.name = "team-max";
    }
    return copy;
  });
  await withPage(t, { accounts: renamed, registry: seedRegistry }, async ({ container }) => {
    const select = selectOf(container, "personal");
    assert.deepEqual(
      [...select.options].map((option) => option.textContent),
      ["the session's own login", "max-1", "team-max", "max-3", "pro-1"],
    );
    assert.deepEqual([...select.options].map((option) => option.value), ["", "max-1", "max-2", "max-3", "pro-1"]);
    assert.equal(select.value, "max-1");
    assert.equal(selectOf(container, "work").value, "");
  });
});

test("RoutesWebAccount: a card draws its id, a default tag on the default context only, its default seat, its model count, and its subagents' seats when set", async (t) => {
  await withPage(t, seed, async ({ container }) => {
    const personal = cardOf(container, "personal");
    const work = cardOf(container, "work");
    assert.equal(personal.querySelector(".ctx-name")?.textContent, "personal");
    assert.deepEqual([...personal.querySelectorAll(".chip")].map((tag) => tag.textContent), ["default"]);
    assert.deepEqual(factsOf(personal), [
      "default seat anthropic",
      "models 2",
      "subagents anthropic, Explore anthropic-haiku",
    ]);
    assert.equal(work.querySelector(".ctx-name")?.textContent, "work");
    assertNone(work.querySelector(".chip"));
    assert.deepEqual(factsOf(work), ["default seat work-bedrock", "models 0"]);
  });
});
