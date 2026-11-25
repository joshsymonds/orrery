import assert from "node:assert/strict";
import { type TestContext, test } from "node:test";

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
import type { AccountsAddRequest } from "./gen/orrery/v1/accounts_pb";
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
// check. The page is given the clients as made, or as given changes them. The page is unmounted and the DOM removed when
// check returns or throws.
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
    await check({ fake, container, settle });
  } finally {
    await teardown();
  }
}

// seed is the fixture's accounts and registry: personal uses max-1, and the other accounts are used by no context.
const seed: AccountsSeed = { accounts: seedAccounts, registry: seedRegistry };

// oatToken is an invented setup-token, which the form takes. apiKeyToken is an invented API key, which orreryd refuses.
const oatToken = "sk-ant-oat01-invented-token-one";
const apiKeyToken = "sk-ant-api03-invented-token-two";

// apiKeyRefusal is orreryd's message for an API key pasted where a setup-token belongs, which serve passes through.
const apiKeyRefusal = "That is an API key, not a setup-token. A setup-token starts sk-ant-oat.";

// newId is the id the fake gives the first account added to it: 32 hex digits, ending in 1.
const newId = "1".padStart(32, "0");

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

// cellsOf is the text of each cell of an account row, before its actions cell.
function cellsOf(row: HTMLTableRowElement): string[] {
  return [...row.querySelectorAll("td")].slice(0, -1).map((cell) => cell.textContent ?? "");
}

// formOf is the Add account form the page draws, or null while the form is closed.
function formOf(container: HTMLElement): HTMLElement | null {
  return container.querySelector<HTMLElement>(".acc-add");
}

// openedForm is the Add account form, which must be drawn.
function openedForm(container: HTMLElement): HTMLElement {
  const form = formOf(container);
  assert.ok(form !== null, "the Add account form is not drawn");
  return form;
}

// headAddButton is the Add account button in the page head, which the page draws once its read has held.
function headAddButton(container: HTMLElement): HTMLButtonElement {
  const button = [...container.querySelectorAll<HTMLButtonElement>(".ptitle button")].find(
    (candidate) => candidate.textContent === "Add account",
  );
  assert.ok(button !== undefined, "the page head draws no Add account button");
  return button;
}

// buttonIn is the button of scope labelled label, which scope must draw.
function buttonIn(scope: HTMLElement, label: string): HTMLButtonElement {
  const button = [...scope.querySelectorAll<HTMLButtonElement>("button")].find(
    (candidate) => candidate.textContent === label,
  );
  assert.ok(button !== undefined, `the form draws no ${label} button`);
  return button;
}

// buttonsOf is the label of each button in scope.
function buttonsOf(scope: HTMLElement): string[] {
  return [...scope.querySelectorAll("button")].map((button) => button.textContent ?? "");
}

// controlIn is the control of form labelled label: the element selector matches within the field whose label reads label.
function controlIn<T extends HTMLElement>(form: HTMLElement, label: string, selector: string): T {
  const control = [...form.querySelectorAll("label")]
    .find((candidate) => candidate.querySelector(".lbl")?.textContent === label)
    ?.querySelector<T>(selector);
  assert.ok(control !== null && control !== undefined, `the form draws no ${label} ${selector}`);
  return control;
}

// optionsOf is the value and text of each option of select.
function optionsOf(select: HTMLSelectElement): string[][] {
  return [...select.querySelectorAll("option")].map((option) => [option.value, option.textContent ?? ""]);
}

// refusalOf is the line a refused Add drew under the form, or null while none is drawn.
function refusalOf(container: HTMLElement): string | null {
  return container.querySelector(".acc-add .help[data-tone='bad']")?.textContent ?? null;
}

// addNoteOf is the line the page draws under its table after a write with no answer, or null while none is drawn.
function addNoteOf(container: HTMLElement): string | null {
  return container.querySelector("section > p.acc-note")?.textContent ?? null;
}

// press clicks element, and lets Preact draw what the click changed.
async function press(element: HTMLElement): Promise<void> {
  await act(() => {
    element.dispatchEvent(new Event("click", { bubbles: true, cancelable: true }));
  });
}

// typeIn sets the value of the field labelled label in form as a keystroke does, and lets Preact draw the change.
async function typeIn(form: HTMLElement, label: string, value: string): Promise<void> {
  const field = controlIn<HTMLInputElement>(form, label, "input");
  await act(() => {
    field.value = value;
    field.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

// chooseIn stores the option value in the select labelled label in form, as a choice does, and lets Preact draw the change.
async function chooseIn(form: HTMLElement, label: string, value: string): Promise<void> {
  const select = controlIn<HTMLSelectElement>(form, label, "select");
  await act(() => {
    select.value = value;
    select.dispatchEvent(new Event("change", { bubbles: true }));
  });
}

// methodsSince is the method of each call the fake received from its call number start on.
function methodsSince(fake: AccountsServe, start: number): string[] {
  return fake.calls.slice(start).map((call) => call.method);
}

// addsSince is the request of each Accounts.Add the fake received from its call number start on.
function addsSince(fake: AccountsServe, start: number): AccountsAddRequest[] {
  return fake.calls.slice(start).flatMap((call) => (call.method === "Accounts.Add" ? [call.request] : []));
}

test("AccountsWebAdd: Add account in the page head opens the form under the Accounts table, after the table", async (t) => {
  await withPage(t, seed, async ({ container }) => {
    assertNone(formOf(container), "the form is drawn before Add account is pressed");
    await press(headAddButton(container));
    const form = openedForm(container);
    assert.equal(form.previousElementSibling?.classList.contains("acc-wrap"), true, "the form does not follow the table");
  });
});

test("AccountsWebAdd: the form draws a Name, a Provider, a Plan and a Token, with Add account and Cancel", async (t) => {
  await withPage(t, seed, async ({ container }) => {
    await press(headAddButton(container));
    const form = openedForm(container);
    assert.deepEqual(
      [...form.querySelectorAll(".acc-add-field > .lbl")].map((label) => label.textContent),
      ["Name", "Provider", "Plan", "Token"],
    );
    assert.deepEqual(buttonsOf(form), ["Add account", "Cancel"]);
  });
});

test("AccountsWebAdd: Provider is one option, Claude subscription, whose value is anthropic", async (t) => {
  await withPage(t, seed, async ({ container }) => {
    await press(headAddButton(container));
    const provider = controlIn<HTMLSelectElement>(openedForm(container), "Provider", "select");
    assert.deepEqual(optionsOf(provider), [["anthropic", "Claude subscription"]]);
    assert.equal(provider.value, "anthropic");
  });
});

test("AccountsWebAdd: Plan offers Claude Max and Claude Pro, and stores the one chosen exactly", async (t) => {
  await withPage(t, seed, async ({ container }) => {
    await press(headAddButton(container));
    const form = openedForm(container);
    const plan = controlIn<HTMLSelectElement>(form, "Plan", "select");
    assert.deepEqual(optionsOf(plan), [
      ["Claude Max", "Claude Max"],
      ["Claude Pro", "Claude Pro"],
    ]);
    assert.equal(plan.value, "Claude Max", "the default plan");
    await chooseIn(form, "Plan", "Claude Pro");
    assert.equal(controlIn<HTMLSelectElement>(form, "Plan", "select").value, "Claude Pro");
  });
});

test("AccountsWebAdd: the Token is a masked password field with autocomplete and spellcheck off, under the hint on where it comes from", async (t) => {
  await withPage(t, seed, async ({ container }) => {
    await press(headAddButton(container));
    const form = openedForm(container);
    const token = controlIn<HTMLInputElement>(form, "Token", "input");
    assert.equal(token.getAttribute("type"), "password");
    assert.equal(token.getAttribute("autocomplete"), "off");
    assert.equal(token.getAttribute("spellcheck"), "false");
    assert.equal(
      form.querySelector(".help")?.textContent,
      "Run claude setup-token on any machine with a browser, sign in to the account, and paste what it prints. It lasts a year; orrery shows only its hash.",
    );
  });
});

test("AccountsWebAdd: Add sends the name and the pasted token as given, with provider anthropic and the plan Claude Max", async (t) => {
  await withPage(t, seed, async ({ container, fake, settle }) => {
    await press(headAddButton(container));
    const form = openedForm(container);
    await typeIn(form, "Name", "max-4");
    await typeIn(form, "Token", oatToken);
    const before = fake.calls.length;
    await press(buttonIn(form, "Add account"));
    await settle(() => formOf(container) === null, "the form closed");
    assert.deepEqual(
      addsSince(fake, before).map((request) => ({
        name: request.name,
        provider: request.provider,
        plan: request.plan,
        token: request.token,
      })),
      [{ name: "max-4", provider: "anthropic", plan: "Claude Max", token: oatToken }],
    );
  });
});

test("AccountsWebAdd: Add sends the plan the Plan select stores, Claude Pro when that is chosen", async (t) => {
  await withPage(t, seed, async ({ container, fake, settle }) => {
    await press(headAddButton(container));
    const form = openedForm(container);
    await typeIn(form, "Name", "pro-2");
    await chooseIn(form, "Plan", "Claude Pro");
    await typeIn(form, "Token", oatToken);
    const before = fake.calls.length;
    await press(buttonIn(form, "Add account"));
    await settle(() => formOf(container) === null, "the form closed");
    assert.deepEqual(
      addsSince(fake, before).map((request) => request.plan),
      ["Claude Pro"],
    );
  });
});

test("AccountsWebAdd: an answered Add closes the form with every field emptied, and the account it answers appears in the table", async (t) => {
  await withPage(t, seed, async ({ container, settle }) => {
    await press(headAddButton(container));
    const form = openedForm(container);
    await typeIn(form, "Name", "max-4");
    await typeIn(form, "Token", oatToken);
    await press(buttonIn(form, "Add account"));
    await settle(() => formOf(container) === null && container.querySelector(`tr[data-account="${newId}"]`) !== null, "the closed form and the added row");
    assert.deepEqual(cellsOf(rowOf(container, newId)), ["max-4", "Claude Max", "#f00d00", "none", "Nov 15"]);
    assert.equal(addNoteOf(container), null, "an answered Add draws no note");
    await press(headAddButton(container));
    const reopened = openedForm(container);
    assert.deepEqual(
      [controlIn<HTMLInputElement>(reopened, "Name", "input").value, controlIn<HTMLInputElement>(reopened, "Token", "input").value],
      ["", ""],
    );
    assert.equal(controlIn<HTMLSelectElement>(reopened, "Plan", "select").value, "Claude Max");
  });
});

test("AccountsWebAdd: Cancel closes the form, empties every field and sends nothing", async (t) => {
  await withPage(t, seed, async ({ container, fake }) => {
    await press(headAddButton(container));
    const form = openedForm(container);
    await typeIn(form, "Name", "max-4");
    await typeIn(form, "Token", oatToken);
    await chooseIn(form, "Plan", "Claude Pro");
    const before = fake.calls.length;
    await press(buttonIn(form, "Cancel"));
    assertNone(formOf(container), "the form is still drawn after Cancel");
    assert.deepEqual(methodsSince(fake, before), []);
    await press(headAddButton(container));
    const reopened = openedForm(container);
    assert.deepEqual(
      [controlIn<HTMLInputElement>(reopened, "Name", "input").value, controlIn<HTMLInputElement>(reopened, "Token", "input").value],
      ["", ""],
    );
    assert.equal(controlIn<HTMLSelectElement>(reopened, "Plan", "select").value, "Claude Max");
  });
});

test("AccountsWebAdd: an Add refused for an invented API key draws Not added with orreryd's reason and Nothing changed, and keeps the form as drawn", async (t) => {
  await withPage(t, seed, async ({ container, fake, settle }) => {
    fake.refuseNext("Accounts.Add", Code.InvalidArgument, apiKeyRefusal);
    await press(headAddButton(container));
    const form = openedForm(container);
    await typeIn(form, "Name", "max-4");
    await typeIn(form, "Token", apiKeyToken);
    const before = fake.calls.length;
    await press(buttonIn(form, "Add account"));
    await settle(() => refusalOf(container) !== null, "the refusal's line");
    assert.equal(refusalOf(container), `Not added: ${apiKeyRefusal} Nothing changed.`);
    const kept = openedForm(container);
    assert.equal(controlIn<HTMLInputElement>(kept, "Name", "input").value, "max-4");
    const token = controlIn<HTMLInputElement>(kept, "Token", "input");
    assert.equal(token.value, apiKeyToken);
    assert.equal(token.getAttribute("type"), "password", "the token is drawn masked");
    assert.ok(token.closest(".field.error") !== null, "the refused token's field is not drawn as refused");
    assert.deepEqual(methodsSince(fake, before), ["Accounts.Add"], "a refused Add re-reads nothing");
    assert.deepEqual(idsOf(container), ["max-1", "max-2", "max-3", "pro-1"]);
  });
});

test("AccountsWebAdd: a refused Add draws no token as text, in the page's text or its markup", async (t) => {
  await withPage(t, seed, async ({ container, fake, settle }) => {
    fake.refuseNext("Accounts.Add", Code.InvalidArgument, apiKeyRefusal);
    await press(headAddButton(container));
    const form = openedForm(container);
    await typeIn(form, "Name", "max-4");
    await typeIn(form, "Token", apiKeyToken);
    await press(buttonIn(form, "Add account"));
    await settle(() => refusalOf(container) !== null, "the refusal's line");
    assert.equal(container.textContent?.includes(apiKeyToken), false);
    assert.equal(container.innerHTML.includes(apiKeyToken), false);
  });
});

test("AccountsWebAdd: an Add with no answer closes the form, empties its token, re-reads the accounts and routes, and says it could not tell", async (t) => {
  await withPage(t, seed, async ({ container, fake, settle }) => {
    fake.dropNext("Accounts.Add");
    await press(headAddButton(container));
    const form = openedForm(container);
    await typeIn(form, "Name", "max-4");
    await typeIn(form, "Token", oatToken);
    const before = fake.calls.length;
    await press(buttonIn(form, "Add account"));
    await settle(
      () => addNoteOf(container) === couldNotTell && methodsSince(fake, before).length === 3,
      "the could-not-tell note and the re-read",
    );
    assertNone(formOf(container), "the form is still drawn after the write had no answer");
    assert.deepEqual(methodsSince(fake, before).sort(), ["Accounts.Add", "Accounts.List", "Routes.Get"]);
    assert.deepEqual(idsOf(container), ["max-1", "max-2", "max-3", "pro-1"]);
    await press(headAddButton(container));
    const reopened = openedForm(container);
    assert.equal(controlIn<HTMLInputElement>(reopened, "Token", "input").value, "", "the token was not emptied");
  });
});

test("AccountsWebAdd: an Add with no answer that landed draws the account the re-read finds, and says it could not tell", async (t) => {
  // landsThenDrops gives the page an Accounts client whose Add reaches the fake, which adds the account, and then fails as a
  // deadline does, so the page has no answer and must read what stands.
  const landsThenDrops = (clients: ReturnType<typeof createClients>): ReturnType<typeof createClients> => ({
    ...clients,
    accounts: {
      ...clients.accounts,
      add: async (request) => {
        await clients.accounts.add(request);
        throw new ConnectError("the answer was lost", Code.DeadlineExceeded);
      },
    },
  });
  await withPage(
    t,
    seed,
    async ({ container, settle }) => {
      await press(headAddButton(container));
      const form = openedForm(container);
      await typeIn(form, "Name", "max-4");
      await typeIn(form, "Token", oatToken);
      await press(buttonIn(form, "Add account"));
      await settle(
        () => addNoteOf(container) === couldNotTell && container.querySelector(`tr[data-account="${newId}"]`) !== null,
        "the could-not-tell note and the re-read row",
      );
      assert.deepEqual(cellsOf(rowOf(container, newId))[0], "max-4");
    },
    landsThenDrops,
  );
});
