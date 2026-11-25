import assert from "node:assert/strict";
import { type TestContext, test } from "node:test";
import { format } from "node:util";

import { createRouterTransport } from "@connectrpc/connect";
import { act } from "preact/test-utils";

import { createClients } from "./client";
import { accounts as seedAccounts, accountsServe, registry as seedRegistry } from "./fixtures/accounts";
import { start } from "./main";
import { mockTimers } from "./testclock";
import { assertNone, installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until } from "./testwait";

// wideWidth and narrowWidth are the window widths on either side of the 760px query the page's layout follows.
const wideWidth = 1024;
const narrowWidth = 700;
// longPollMs is a poll interval no test reaches, so only the page's own reads run.
const longPollMs = 600_000;
// accountsHash is the address of the accounts page, and inboxHash the address a test leaves the page for.
const accountsHash = "#/accounts";
const inboxHash = "#/inbox";
// marker is the invented setup-token every test pastes. Once the form is done with, no input's value, no DOM text or
// attribute, no address, no history state and no console call may hold it.
const marker = "sk-ant-oat01-invented-marker-token";
// consoleMethods are the console methods a test replaces to capture what the page logs.
const consoleMethods = ["debug", "error", "info", "log", "trace", "warn"] as const;

// Page is the accounts page the App draws at accountsHash: root is the element it draws into, dom the window the page runs
// in, and logged the arguments of each console call made while the test runs. settle waits until check holds, letting the
// page draw between checks. leave moves the page to the inbox's address, and returnToPage moves it back to the accounts
// page's, as the browser's Back and Forward do.
interface Page {
  root: HTMLElement;
  dom: ReturnType<typeof installTestDom>;
  logged: unknown[][];
  settle(check: () => boolean, what: string): Promise<void>;
  leave(): Promise<void>;
  returnToPage(): Promise<void>;
}

// captureConsole replaces each console method for the test, which node:test restores when the test ends, and returns the
// arguments of every call made while they are replaced.
function captureConsole(t: TestContext): unknown[][] {
  const logged: unknown[][] = [];
  for (const name of consoleMethods) {
    t.mock.method(console, name, (...args: unknown[]) => {
      logged.push(args);
    });
  }
  return logged;
}

// goTo moves dom to hash as Back and Forward do: the history takes the hash, and the page is told it changed.
async function goTo(dom: ReturnType<typeof installTestDom>, hash: string): Promise<void> {
  await act(() => {
    dom.history.pushState(null, "", hash);
    dom.dispatchEvent(new dom.Event("popstate"));
  });
}

// withPage mounts the accounts page over a fake of the accounts and routes, at width, with its timers on the fake clock, and
// runs check. The page is unmounted and the DOM removed when check returns or throws.
async function withPage(t: TestContext, width: number, check: (page: Page) => Promise<void>): Promise<void> {
  mockTimers(t, ["setTimeout", "setInterval"]);
  const dom = installTestDom();
  dom.innerWidth = width;
  dom.history.replaceState(null, "", accountsHash);
  const logged = captureConsole(t);
  const fake = accountsServe({ accounts: seedAccounts, registry: seedRegistry });
  const clients = createClients(createRouterTransport((router) => fake.register(router)));
  const root = document.createElement("div");
  document.body.append(root);
  const stop = start(root, clients, longPollMs);
  const teardown = teardownOnce(t.signal, async () => {
    await act(() => {
      stop();
    });
    root.remove();
    await removeTestDom(dom);
  });
  const settle = (ready: () => boolean, what: string) => until(t.signal, ready, what, { step: () => act(turn) });
  try {
    await settle(() => accountsDrawn(root), "the accounts list");
    await check({
      root,
      dom,
      logged,
      settle,
      leave: async () => {
        await goTo(dom, inboxHash);
        await settle(() => root.querySelector(".accounts-read") === null, "the accounts page to shut");
      },
      returnToPage: async () => {
        await goTo(dom, accountsHash);
        await settle(() => accountsDrawn(root), "the accounts page to draw again");
      },
    });
  } finally {
    await teardown();
  }
}

// accountsDrawn is whether the Accounts list draws the fixture's four accounts.
function accountsDrawn(root: HTMLElement): boolean {
  return root.querySelectorAll('section[aria-label="Accounts"] [data-account]').length === 4;
}

// press clicks element, and lets Preact draw what the click changed.
async function press(element: HTMLElement): Promise<void> {
  await act(() => {
    element.dispatchEvent(new Event("click", { bubbles: true, cancelable: true }));
  });
}

// pressKey sends key to element as a keydown that bubbles, and lets Preact draw what the key changed.
async function pressKey(element: HTMLElement, key: string): Promise<void> {
  await act(() => {
    element.dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true }));
  });
}

// pointerDownOn sends a press to element, as a pointer going down does, and lets Preact draw what the press changed.
async function pointerDownOn(element: HTMLElement): Promise<void> {
  await act(() => {
    element.dispatchEvent(new Event("pointerdown", { bubbles: true, cancelable: true }));
  });
}

// formOf is the Add account form the page draws, in the table's place or in the sheet, or null while it is closed.
function formOf(root: HTMLElement): HTMLElement | null {
  return root.querySelector<HTMLElement>(".acc-add");
}

// headAddOf is the Add account control in the page head: the wide page's labelled button, or the narrow page's + icon.
function headAddOf(root: HTMLElement): HTMLButtonElement {
  const button = [...root.querySelectorAll<HTMLButtonElement>(".ptitle button")].find(
    (candidate) => candidate.getAttribute("aria-label") === "Add account" || candidate.textContent?.trim() === "Add account",
  );
  assert.ok(button !== undefined, "the page head draws no Add account control");
  return button;
}

// openForm presses the head's Add account control, and returns the form it opens.
async function openForm(page: Page): Promise<HTMLElement> {
  await press(headAddOf(page.root));
  const form = formOf(page.root);
  assert.ok(form !== null, "pressing Add account draws no form");
  return form;
}

// fieldIn is the input of form labelled label, as the form draws it.
function fieldIn(form: HTMLElement, label: string): HTMLInputElement {
  const field = [...form.querySelectorAll("label")]
    .find((candidate) => candidate.querySelector(".lbl")?.textContent === label)
    ?.querySelector<HTMLInputElement>("input");
  assert.ok(field !== null && field !== undefined, `the form draws no ${label} input`);
  return field;
}

// buttonIn is the button of form whose text is label, which form must draw.
function buttonIn(form: HTMLElement, label: string): HTMLButtonElement {
  const button = [...form.querySelectorAll<HTMLButtonElement>("button")].find(
    (candidate) => candidate.textContent?.trim() === label,
  );
  assert.ok(button !== undefined, `the form draws no ${label} button`);
  return button;
}

// typeIn sets the value of the field labelled label in form as a keystroke does, and lets Preact draw the change.
async function typeIn(form: HTMLElement, label: string, value: string): Promise<void> {
  const field = fieldIn(form, label);
  await act(() => {
    field.value = value;
    field.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

// fillForm types an invented name and the marker token into form.
async function fillForm(form: HTMLElement): Promise<void> {
  await typeIn(form, "Name", "max-4");
  await typeIn(form, "Token", marker);
}

// assertNoMarker fails if the marker is in any input's value, in the DOM's text or attributes, in the address or the history
// state, or in any console call the test captured.
function assertNoMarker(page: Page): void {
  for (const input of document.querySelectorAll("input")) {
    assert.equal(input.value.includes(marker), false, "an input holds the token");
  }
  assert.equal(document.body.innerHTML.includes(marker), false, "the DOM holds the token in its text or attributes");
  assert.equal(page.dom.location.hash.includes(marker), false, "the address holds the token");
  assert.equal(JSON.stringify(page.dom.history.state ?? null).includes(marker), false, "the history state holds the token");
  assert.equal(
    page.logged.some((args) => {
      const [first = "", ...rest] = args;
      return format(first, ...rest).includes(marker);
    }),
    false,
    "a console call holds the token",
  );
}

// assertReopenedEmpty fails unless the token field of form is empty and of type password.
function assertReopenedEmpty(form: HTMLElement): void {
  const token = fieldIn(form, "Token");
  assert.equal(token.getAttribute("type"), "password", "the reopened token field is not masked");
  assert.equal(token.value, "", "the reopened token field holds a token");
}

// sheetOf is the sheet the narrow page raises over itself for the form, or null while no sheet is raised.
function sheetOf(root: HTMLElement): HTMLElement | null {
  return root.querySelector<HTMLElement>('[role="dialog"]');
}

test("AccountsWebNoToken: a stored Add in the table's form leaves the token in no field, the DOM, the address or the console, and the reopened token field is empty and masked", (t) =>
  withPage(t, wideWidth, async (page) => {
    const form = await openForm(page);
    await fillForm(form);
    await press(buttonIn(form, "Add account"));
    await page.settle(
      () => formOf(page.root) === null && page.root.querySelectorAll('section[aria-label="Accounts"] [data-account]').length === 5,
      "the form to close with the added row",
    );
    assertNoMarker(page);
    assertReopenedEmpty(await openForm(page));
    assertNoMarker(page);
  }));

test("AccountsWebNoToken: Cancel in the table's form leaves the token in no field, the DOM, the address or the console, and the reopened token field is empty and masked", (t) =>
  withPage(t, wideWidth, async (page) => {
    const form = await openForm(page);
    await fillForm(form);
    await press(buttonIn(form, "Cancel"));
    assertNone(formOf(page.root), "the form is still drawn after Cancel");
    assertNoMarker(page);
    assertReopenedEmpty(await openForm(page));
    assertNoMarker(page);
  }));

test("AccountsWebNoToken: Escape in the token field closes the table's form, and the token leaves with it", (t) =>
  withPage(t, wideWidth, async (page) => {
    const form = await openForm(page);
    await fillForm(form);
    const token = fieldIn(form, "Token");
    token.focus();
    await pressKey(token, "Escape");
    assertNone(formOf(page.root), "the form is still drawn after Escape");
    assertNoMarker(page);
    assertReopenedEmpty(await openForm(page));
    assertNoMarker(page);
  }));

test("AccountsWebNoToken: Escape with the focus on the page head closes the table's form, and the token leaves with it", (t) =>
  withPage(t, wideWidth, async (page) => {
    const form = await openForm(page);
    await fillForm(form);
    const head = headAddOf(page.root);
    head.focus();
    await pressKey(head, "Escape");
    assertNone(formOf(page.root), "the form is still drawn after Escape from the page");
    assertNoMarker(page);
    assertReopenedEmpty(await openForm(page));
    assertNoMarker(page);
  }));

test("AccountsWebNoToken: leaving the page through its address and returning draws it with the form closed, and the token field empty and masked", (t) =>
  withPage(t, wideWidth, async (page) => {
    const form = await openForm(page);
    await fillForm(form);
    await page.leave();
    assertNoMarker(page);
    await page.returnToPage();
    assertNone(formOf(page.root), "the form is drawn on return");
    assertNoMarker(page);
    assertReopenedEmpty(await openForm(page));
    assertNoMarker(page);
  }));

test("AccountsWebNoToken: under 760 px a stored Add in the sheet leaves the token in no field, the DOM, the address or the console, and the reopened token field is empty and masked", (t) =>
  withPage(t, narrowWidth, async (page) => {
    const form = await openForm(page);
    await fillForm(form);
    await press(buttonIn(form, "Add account"));
    await page.settle(
      () => sheetOf(page.root) === null && page.root.querySelectorAll('section[aria-label="Accounts"] [data-account]').length === 5,
      "the sheet to close with the added row",
    );
    assertNoMarker(page);
    assertReopenedEmpty(await openForm(page));
    assertNoMarker(page);
  }));

test("AccountsWebNoToken: under 760 px Cancel in the sheet leaves the token in no field, the DOM, the address or the console, and the reopened token field is empty and masked", (t) =>
  withPage(t, narrowWidth, async (page) => {
    const form = await openForm(page);
    await fillForm(form);
    await press(buttonIn(form, "Cancel"));
    assertNone(sheetOf(page.root), "the sheet is still drawn after Cancel");
    assertNoMarker(page);
    assertReopenedEmpty(await openForm(page));
    assertNoMarker(page);
  }));

test("AccountsWebNoToken: under 760 px Escape in the sheet closes it, and the token leaves with it", (t) =>
  withPage(t, narrowWidth, async (page) => {
    const form = await openForm(page);
    await fillForm(form);
    const token = fieldIn(form, "Token");
    token.focus();
    await pressKey(token, "Escape");
    assertNone(sheetOf(page.root), "the sheet is still drawn after Escape");
    assertNoMarker(page);
    assertReopenedEmpty(await openForm(page));
    assertNoMarker(page);
  }));

test("AccountsWebNoToken: under 760 px a press on the sheet's scrim closes it, and the token leaves with it", (t) =>
  withPage(t, narrowWidth, async (page) => {
    const form = await openForm(page);
    await fillForm(form);
    const scrim = page.root.querySelector<HTMLElement>(".sheet-scrim");
    assert.ok(scrim !== null, "the sheet draws no scrim");
    await pointerDownOn(scrim);
    assertNone(sheetOf(page.root), "the sheet is still drawn after a press on the scrim");
    assertNoMarker(page);
    assertReopenedEmpty(await openForm(page));
    assertNoMarker(page);
  }));

test("AccountsWebNoToken: under 760 px leaving the page through its address and returning draws it with the sheet closed, and the token field empty and masked", (t) =>
  withPage(t, narrowWidth, async (page) => {
    const form = await openForm(page);
    await fillForm(form);
    await page.leave();
    assertNoMarker(page);
    await page.returnToPage();
    assertNone(sheetOf(page.root), "the sheet is drawn on return");
    assertNoMarker(page);
    assertReopenedEmpty(await openForm(page));
    assertNoMarker(page);
  }));
