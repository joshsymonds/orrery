import assert from "node:assert/strict";
import { type TestContext, test } from "node:test";

import { Code, createRouterTransport } from "@connectrpc/connect";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients } from "./client";
import {
  type AccountsServe,
  type AccountsSeed,
  accounts as seedAccounts,
  accountsServe,
  registry as seedRegistry,
} from "./fixtures/accounts";
import { start as startPage } from "./main";
import { type Address, formatAddress } from "./router";
import type { SeenClock } from "./seen";
import { mockTimers } from "./testclock";
import { assertNone, installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until } from "./testwait";

// wideWidth and narrowWidth are the window widths on either side of the 760px query the page's layout follows.
const wideWidth = 1024;
const narrowWidth = 700;
const now = 1000;
// longPollMs is a poll interval no test reaches, so only the page's own reads run.
const longPollMs = 600_000;

// seed is the fixture's accounts and registry: personal uses max-1, work uses no account, and the other accounts are used by no
// context.
const seed: AccountsSeed = { accounts: seedAccounts, registry: seedRegistry };

// accountsAddress is the address of the accounts page.
const accountsAddress: Address = { view: "inbox", entry: { kind: "accounts" } };

// stillClock is a seen clock whose timers never fire, so no row is marked seen.
const stillClock: SeenClock = {
  now: () => now * 1000,
  setTimeout: () => 0,
  clearTimeout: () => {},
};

// Report is one address App reports: the hash it writes, and whether it pushed it.
interface Report {
  form: string;
  mode: "push" | "replace";
}

// Mounted is the App drawn over the fake, with what a test checks: the container it draws into, the fake, the addresses App
// reported, and settle, which waits until check holds, letting the App draw between checks.
interface Mounted {
  container: HTMLElement;
  fake: AccountsServe;
  reports: Report[];
  settle(check: () => boolean, what: string): Promise<void>;
}

// withApp mounts the App over a fake seeded with seed, at width, opened at address when one is given, in a fresh DOM with its
// timers on the fake clock, and runs check. The App is unmounted and the DOM removed when check returns or throws.
async function withApp(
  t: TestContext,
  options: { width: number; address?: Address },
  check: (mounted: Mounted) => Promise<void>,
): Promise<void> {
  mockTimers(t, ["setTimeout", "setInterval"]);
  const window = installTestDom();
  window.innerWidth = options.width;
  const fake = accountsServe(seed);
  const clients = createClients(createRouterTransport((router) => fake.register(router)));
  const reports: Report[] = [];
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
      render(
        <App
          rows={[]}
          now={now}
          clients={clients}
          seenClock={stillClock}
          address={options.address}
          onAddress={(address, mode) => reports.push({ form: formatAddress(address), mode })}
        />,
        container,
      );
    });
    await check({ container, fake, reports, settle });
  } finally {
    await teardown();
  }
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

// buttonLabelled is the button under root whose aria-label is label, which root must draw.
function buttonLabelled(root: ParentNode, label: string): HTMLButtonElement {
  const button = root.querySelector<HTMLButtonElement>(`button[aria-label="${label}"]`);
  assert.ok(button !== null, `no button is labelled ${label}`);
  return button;
}

// buttonText is the button under root whose text is text, which root must draw.
function buttonText(root: ParentNode, text: string): HTMLButtonElement {
  const button = [...root.querySelectorAll("button")].find((candidate) => candidate.textContent?.trim() === text);
  assert.ok(button !== undefined, `no button reads ${text}`);
  return button;
}

// pageOf is the accounts page section the App draws, which must be drawn.
function pageOf(container: HTMLElement): HTMLElement {
  const page = container.querySelector<HTMLElement>(".accounts-read");
  assert.ok(page !== null, "the accounts page is not drawn");
  return page;
}

// listOf is the inbox's list column, which the App hides under 760px while a page draws alone.
function listOf(container: HTMLElement): HTMLElement {
  const list = container.querySelector<HTMLElement>(".list");
  assert.ok(list !== null, "the inbox list is not drawn");
  return list;
}

// dialogOf is the open sheet, which must be drawn.
function dialogOf(container: HTMLElement): HTMLElement {
  const dialog = container.querySelector<HTMLElement>('[role="dialog"]');
  assert.ok(dialog !== null, "no sheet is open");
  return dialog;
}

// rowsOf is each row the selector names on the accounts page, as the name and the facts its two lines draw.
function rowsOf(container: HTMLElement, selector: string): string[][] {
  return [...pageOf(container).querySelectorAll<HTMLElement>(selector)].map((row) => [
    row.querySelector(".a")?.textContent ?? "",
    row.querySelector(".b")?.textContent ?? "",
  ]);
}

// accountRows, contextRows and seatRows are the rows of the page's Accounts, Contexts and Seats lists.
const accountRows = (container: HTMLElement) => rowsOf(container, 'section[aria-label="Accounts"] [data-account]');
const contextRows = (container: HTMLElement) => rowsOf(container, 'section[aria-label="Contexts"] [data-context]');
const seatRows = (container: HTMLElement) => rowsOf(container, 'section[aria-label="Seats"] .phli');

// accountNamed is the row the Accounts list draws for the account with id, which must be drawn.
function accountNamed(container: HTMLElement, id: string): HTMLElement {
  const row = pageOf(container).querySelector<HTMLElement>(`[data-account="${id}"]`);
  assert.ok(row !== null, `the Accounts list draws no row for ${id}`);
  return row;
}

// adds, renames and contextSets are the requests the fake received for each write, in order, as plain fields.
function adds(fake: AccountsServe) {
  return fake.calls.flatMap((call) =>
    call.method === "Accounts.Add"
      ? [{ name: call.request.name, provider: call.request.provider, plan: call.request.plan, token: call.request.token }]
      : [],
  );
}
function renames(fake: AccountsServe) {
  return fake.calls.flatMap((call) => (call.method === "Accounts.Rename" ? [{ id: call.request.id, name: call.request.name }] : []));
}
function contextSets(fake: AccountsServe) {
  return fake.calls.flatMap((call) =>
    call.method === "Routes.SetContextAccount" ? [{ context: call.request.context, account: call.request.account }] : [],
  );
}

// accountsDrawn settles until the Accounts list draws four accounts, the fixture's.
function accountsDrawn(mounted: Mounted): Promise<void> {
  return mounted.settle(() => mounted.container.querySelectorAll('section[aria-label="Accounts"] [data-account]').length === 4, "the accounts list");
}

test("AccountsWebWay: the foot gear opens #/accounts, which the App reports as its address and draws in the conversation's place", (t) =>
  withApp(t, { width: wideWidth }, async (mounted) => {
    const { container, reports } = mounted;
    assertNone(container.querySelector(".accounts-read"), "the accounts page is drawn before the gear is pressed");
    const foot = container.querySelector<HTMLElement>(".inbox-foot");
    assert.ok(foot !== null, "the inbox foot is not drawn");
    const labels = [...foot.querySelectorAll("button")].map((button) => button.getAttribute("aria-label") ?? "");
    assert.equal(labels[1], "Accounts and routes", "the gear comes right after Archived");
    assert.match(foot.querySelector("button")?.textContent ?? "", /^Archived/, "the foot's first button is still Archived");

    await press(buttonLabelled(foot, "Accounts and routes"));
    await accountsDrawn(mounted);
    assert.deepEqual(reports.at(-1), { form: "#/accounts", mode: "push" }, "the App reports the accounts address");
    assert.equal(listOf(container).hidden, false, "the inbox list stays beside the page at 760px and wider");
    assert.equal(pageOf(container).querySelector("h2")?.textContent, "Accounts and routes");
    assertNone(pageOf(container).querySelector('button[aria-label="Back"]'), "the wide page has no Back");
  }));

test("AccountsWebWay: loading #/accounts directly, through start, draws the accounts page", async (t) => {
  mockTimers(t, ["setTimeout", "setInterval"]);
  const window = installTestDom();
  window.innerWidth = wideWidth;
  window.history.replaceState(null, "", "#/accounts");
  const root = document.createElement("div");
  document.body.append(root);
  const fake = accountsServe(seed);
  const clients = createClients(createRouterTransport((router) => fake.register(router)));
  const stop = startPage(root, clients, longPollMs);
  const teardown = teardownOnce(t.signal, async () => {
    await act(() => {
      stop();
    });
    root.remove();
    await removeTestDom(window);
  });
  const settle = (ready: () => boolean, what: string) => until(t.signal, ready, what, { step: () => act(turn) });
  try {
    await settle(() => root.querySelectorAll('section[aria-label="Accounts"] [data-account]').length === 4, "the accounts list");
    assert.equal(window.location.hash, "#/accounts", "the hash still names the accounts page");
    assert.equal(root.querySelector(".accounts-read h2")?.textContent, "Accounts and routes");
    assert.deepEqual(
      fake.calls.map((call) => call.method).sort(),
      ["Accounts.List", "Accounts.List", "Routes.Get", "Routes.Get"],
      "the page reads the accounts and the routes, and the usage band reads them again",
    );
  } finally {
    await teardown();
  }
});

test("AccountsWebWay: under 760 px the accounts page draws alone as section F, with the accounts, the contexts and the seats summary", (t) =>
  withApp(t, { width: narrowWidth, address: accountsAddress }, async (mounted) => {
    const { container } = mounted;
    await accountsDrawn(mounted);
    assert.equal(listOf(container).hidden, true, "the inbox list is hidden while the page draws alone");
    assertNone(container.querySelector("table"), "the page draws no table under 760px");
    assert.deepEqual(accountRows(container), [
      ["max-1", "Claude Max · #3f9a1c · personal"],
      ["max-2", "Claude Max · #b0217e"],
      ["max-3", "Claude Max · #5d0c44"],
      ["pro-1", "Claude Pro · #e81f05"],
    ]);
    assert.deepEqual(contextRows(container), [
      ["personal", "max-1 · anthropic"],
      ["work", "the session's own login · work-bedrock"],
    ]);
    assert.deepEqual(seatRows(container), [["5 seats", "forward 2 · inject 2 · sigv4 1"]]);
    assert.ok(buttonLabelled(pageOf(container), "Back"), "the page head has a Back");
    assert.ok(buttonLabelled(pageOf(container), "Add account"), "the page head has a +");
  }));

test("AccountsWebWay: under 760 px the page's Back returns to the inbox, which draws its list again", (t) =>
  withApp(t, { width: narrowWidth, address: accountsAddress }, async (mounted) => {
    const { container, reports, settle } = mounted;
    await accountsDrawn(mounted);
    await press(buttonLabelled(pageOf(container), "Back"));
    await settle(() => container.querySelector(".accounts-read") === null, "the page to shut");
    assert.equal(listOf(container).hidden, false, "the inbox list draws again");
    assert.deepEqual(reports.at(-1), { form: "#/inbox", mode: "push" }, "the App reports the inbox address");
  }));

test("AccountsWebWay: under 760 px the head's + opens the Add account sheet, and the account it adds is listed", (t) =>
  withApp(t, { width: narrowWidth, address: accountsAddress }, async (mounted) => {
    const { container, fake, settle } = mounted;
    await accountsDrawn(mounted);
    await press(buttonLabelled(pageOf(container), "Add account"));
    const sheet = dialogOf(container);
    assert.equal(sheet.getAttribute("aria-label"), "Add account", "the sheet is the Add account form");
    const name = sheet.querySelector<HTMLInputElement>('input[type="text"]');
    const token = sheet.querySelector<HTMLInputElement>('input[type="password"]');
    assert.ok(name !== null && token !== null, "the form draws its name and token fields");
    await typeInto(name, "max-4");
    await typeInto(token, "tok-invented");
    await press(buttonText(sheet, "Add account"));
    await settle(() => container.querySelector('[role="dialog"]') === null, "the sheet to close");
    await settle(() => accountRows(container).length === 5, "the added account's row");
    assert.deepEqual(adds(fake), [{ name: "max-4", provider: "anthropic", plan: "Claude Max", token: "tok-invented" }]);
  }));

test("AccountsWebWay: under 760 px an account's ⋯ renames it: Rename turns the name into a field, and Save sends it", (t) =>
  withApp(t, { width: narrowWidth, address: accountsAddress }, async (mounted) => {
    const { container, fake, settle } = mounted;
    await accountsDrawn(mounted);
    await press(buttonLabelled(container, "Actions for max-2"));
    assert.equal(dialogOf(container).getAttribute("aria-label"), "max-2", "the sheet is the account's");
    await press(buttonText(dialogOf(container), "Rename"));
    const field = dialogOf(container).querySelector<HTMLInputElement>('input[aria-label="New name for max-2"]');
    assert.ok(field !== null, "Rename draws the name as a field");
    await typeInto(field, "max-two");
    await press(buttonText(dialogOf(container), "Save"));
    await settle(() => accountNamed(container, "max-2").querySelector(".a")?.textContent === "max-two", "the renamed row");
    assert.deepEqual(renames(fake), [{ id: "max-2", name: "max-two" }]);
  }));

test("AccountsWebWay: under 760 px an account's ⋯ removes it once its sheet asks, and a removal refused as in use names its contexts", (t) =>
  withApp(t, { width: narrowWidth, address: accountsAddress }, async (mounted) => {
    const { container, fake, settle } = mounted;
    await accountsDrawn(mounted);

    await press(buttonLabelled(container, "Actions for pro-1"));
    await press(buttonText(dialogOf(container), "Remove"));
    assert.equal(dialogOf(container).querySelector(".sure")?.textContent, "Remove pro-1?", "Remove asks first");
    assert.equal(fake.calls.some((call) => call.method === "Accounts.Remove"), false, "nothing is sent before the ask is answered");
    await press(buttonText(dialogOf(container), "Keep"));
    assert.equal(fake.calls.some((call) => call.method === "Accounts.Remove"), false, "Keep sends nothing");
    await press(buttonText(dialogOf(container), "Remove"));
    await press(buttonText(dialogOf(container), "Remove"));
    await settle(() => container.querySelector('[role="dialog"]') === null, "the sheet to close");
    await settle(() => accountRows(container).length === 3, "the removed account's row to go");
    assertNone(container.querySelector('[data-account="pro-1"]'), "pro-1 is gone from the list");

    await press(buttonLabelled(container, "Actions for max-1"));
    await press(buttonText(dialogOf(container), "Remove"));
    await press(buttonText(dialogOf(container), "Remove"));
    await settle(() => dialogOf(container).querySelector(".acc-note") !== null, "the refusal's note");
    assert.equal(
      dialogOf(container).querySelector(".acc-note")?.textContent,
      "max-1 is in use by personal. Move them to another account first, then remove it. Nothing changed.",
      "the refusal names the context that uses the account",
    );
    assert.ok(accountNamed(container, "max-1"), "max-1 stays in the list");
  }));

test("AccountsWebWay: under 760 px a context's › opens its account choices, and picking one sets that context's account alone", (t) =>
  withApp(t, { width: narrowWidth, address: accountsAddress }, async (mounted) => {
    const { container, fake, settle } = mounted;
    await settle(() => container.querySelectorAll('section[aria-label="Contexts"] [data-context]').length === 2, "the contexts list");
    await press(buttonLabelled(container, "Account of work"));
    const sheet = dialogOf(container);
    assert.equal(sheet.getAttribute("aria-label"), "work", "the sheet is the context's");
    assert.deepEqual(
      [...sheet.querySelectorAll("button")].map((button) => button.textContent),
      ["the session's own login", "max-1", "max-2", "max-3", "pro-1"],
      "the sheet lists the session's own login and every account",
    );
    assert.equal(buttonText(sheet, "the session's own login").getAttribute("aria-current"), "true", "the current choice is marked");
    await press(buttonText(sheet, "max-2"));
    await settle(() => container.querySelector('[role="dialog"]') === null, "the sheet to close");
    await settle(() => contextRows(container)[1]?.[1] === "max-2 · work-bedrock", "the work row's new account");
    assert.deepEqual(contextSets(fake), [{ context: "work", account: "max-2" }], "only the work context is sent");
    assert.deepEqual(contextRows(container)[0], ["personal", "max-1 · anthropic"], "personal keeps its account");
  }));

test("AccountsWebWay: under 760 px a context's choice the server refuses says why in the sheet and changes nothing", (t) =>
  withApp(t, { width: narrowWidth, address: accountsAddress }, async (mounted) => {
    const { container, fake, settle } = mounted;
    await settle(() => container.querySelectorAll('section[aria-label="Contexts"] [data-context]').length === 2, "the contexts list");
    fake.refuseNext("Routes.SetContextAccount", Code.InvalidArgument, "account: unknown id");
    await press(buttonLabelled(container, "Account of work"));
    await press(buttonText(dialogOf(container), "pro-1"));
    await settle(() => dialogOf(container).querySelector(".acc-note") !== null, "the refusal's note");
    assert.equal(
      dialogOf(container).querySelector(".acc-note")?.textContent,
      "Not set: account: unknown id Nothing changed.",
      "the refusal's reason is shown in the sheet",
    );
    assert.deepEqual(contextRows(container)[1], ["work", "the session's own login · work-bedrock"], "work keeps its account");
  }));
