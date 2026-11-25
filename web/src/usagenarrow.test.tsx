import assert from "node:assert/strict";
import { type TestContext, test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { Code, createRouterTransport } from "@connectrpc/connect";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients } from "./client";
import { type AccountsServe, accountsServe } from "./fixtures/accounts";
import {
  type Account,
  AccountSchema,
  RouteContextSchema,
  type RouteRegistry,
  RouteRegistrySchema,
} from "./gen/orrery/v1/accounts_pb";
import type { SeenClock } from "./seen";
import { mockTimers } from "./testclock";
import { assertNone, installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until } from "./testwait";
import { noUsage, type UsageData } from "./usagedata";

// now is the fake clock, in unix seconds. Every time the checks draw is set from it, and no check reads the wall clock.
const now = 1_800_000_000;

// narrowQuery is the query the App's wide flag reads: the window is wide at 760px and wider, and narrow below it.
const narrowQuery = "(min-width: 760px)";

// narrowWidth is the window width the checks draw at, under 760px.
const narrowWidth = 390;

const noop = () => undefined;

// stillClock is a seen clock whose timers never fire, so the App's toast stays until a check takes it away.
const stillClock: SeenClock = {
  now: () => now * 1000,
  setTimeout: () => 0,
  clearTimeout: () => {},
};

// accounts are the invented accounts the checks use: three Claude Max accounts, each named as its id.
const accounts: Account[] = ["max-1", "max-2", "max-3"].map((id) =>
  create(AccountSchema, { id, name: id, provider: "claude", plan: "Claude Max" }),
);

// registryOf is the route registry holding each context with the account given for it, in the order given. The seat each
// context names is invented.
function registryOf(contexts: Readonly<Record<string, string>>): RouteRegistry {
  return create(RouteRegistrySchema, {
    contexts: Object.entries(contexts).map(([id, account]) =>
      create(RouteContextSchema, { id, account, defaultSeat: "example-seat" }),
    ),
  });
}

// usageOf is the usage the checks draw: the accounts, and the registry of the contexts given, with no limit reading.
function usageOf(contexts: Readonly<Record<string, string>>): UsageData {
  return { ...noUsage, accounts, registry: registryOf(contexts), answered: true };
}

// Query is the query list a window's matchMedia returns.
type Query = ReturnType<ReturnType<typeof installTestDom>["matchMedia"]>;

// notMatching is the query list the window gives the wide query under 760px: it matches nothing, and never changes.
function notMatching(media: string): Query {
  return {
    media,
    matches: false,
    addEventListener: () => undefined,
    removeEventListener: () => undefined,
  } as unknown as Query;
}

// narrowMedia makes the window's wide query match nothing, as a window under 760px does, and leaves every other query to the
// window.
function narrowMedia(window: ReturnType<typeof installTestDom>): void {
  const matchMedia = window.matchMedia.bind(window);
  window.matchMedia = (query: string) => (query === narrowQuery ? notMatching(query) : matchMedia(query));
}

// Drawn is a check's view of what it drew: the fake of the accounts and routes, the DOM the page draws into, and settle, which
// waits until ready holds, letting the page draw between checks.
interface Drawn {
  fake: AccountsServe;
  container: HTMLElement;
  settle(ready: () => boolean, what: string): Promise<void>;
}

// withNarrowApp draws the App under 760px, over a fake seeded with contexts, on a fake clock, and runs check. The App is
// unmounted and the DOM removed when check returns or throws.
async function withNarrowApp(
  t: TestContext,
  contexts: Readonly<Record<string, string>>,
  check: (drawn: Drawn) => Promise<void>,
): Promise<void> {
  mockTimers(t, ["setTimeout", "setInterval"]);
  const window = installTestDom();
  window.innerWidth = narrowWidth;
  narrowMedia(window);
  const fake = accountsServe({ accounts, registry: registryOf(contexts) });
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
      render(
        <App
          rows={[]}
          now={now}
          clients={clients}
          seenClock={stillClock}
          usage={usageOf(contexts)}
          onAddress={noop}
        />,
        container,
      );
    });
    await check({ fake, container, settle });
  } finally {
    await teardown();
  }
}

// one returns the first element under root that matches selector, which must be there.
function one(root: ParentNode, selector: string): HTMLElement {
  const found = root.querySelector<HTMLElement>(selector);
  assert.ok(found !== null, `no element matches ${selector}`);
  return found;
}

// sheetTitled is the sheet under root whose title is title, which must be drawn.
function sheetTitled(root: ParentNode, title: string): HTMLElement {
  return one(root, `[role="dialog"][aria-label="${title}"]`);
}

// rowOf is the accounts' row of the account with id.
function rowOf(root: ParentNode, id: string): HTMLElement {
  return one(root, `.up-row[data-account="${id}"]`);
}

// buttonIn is the button under root whose text is label, which must be there.
function buttonIn(root: ParentNode, label: string): HTMLButtonElement {
  const found = [...root.querySelectorAll<HTMLButtonElement>("button")].find((button) => button.textContent === label);
  assert.ok(found !== undefined, `no ${label} button`);
  return found;
}

// setCalls are the SetContextAccount calls the fake received, in order, as the context and the account each one set.
function setCalls(fake: AccountsServe): { context: string; account: string }[] {
  return fake.calls.flatMap((call) =>
    call.method === "Routes.SetContextAccount" ? [{ context: call.request.context, account: call.request.account }] : [],
  );
}

// press clicks the element, and lets Preact draw what the click changed.
async function press(element: Element): Promise<void> {
  await act(() => {
    element.dispatchEvent(new Event("click", { bubbles: true, cancelable: true }));
  });
}

test("UsageNarrow: a tap on the band opens the accounts as a Sheet, not as a popover beside the band", async (t: TestContext) => {
  await withNarrowApp(t, { personal: "max-1", tiltyard: "max-2" }, async ({ container }) => {
    assertNone(
      container.querySelector('[role="dialog"][aria-label="Claude accounts"]'),
      "the accounts are open before the band is pressed",
    );
    await press(one(container, ".uband"));
    const sheet = sheetTitled(container, "Claude accounts");
    assert.ok(sheet.classList.contains("sheet"), "the accounts are not drawn in a Sheet");
    assert.equal(
      sheet.querySelectorAll(".up-row").length,
      accounts.length,
      "the Sheet does not draw a row for each account",
    );
    assertNone(container.querySelector(".upop"), "the popover is drawn beside the band under 760px");
  });
});

test("UsageNarrow: Use asks in a Sheet of its own, naming the moving contexts and their source, and its Move makes the calls", async (t: TestContext) => {
  await withNarrowApp(t, { personal: "max-1", tiltyard: "max-1", work: "" }, async ({ container, fake, settle }) => {
    await press(one(container, ".uband"));
    await press(buttonIn(rowOf(container, "max-2"), "Use"));
    assertNone(
      container.querySelector('[role="dialog"][aria-label="Claude accounts"]'),
      "the accounts Sheet is still drawn under the Use Sheet",
    );
    assertNone(container.querySelector(".up-ask"), "Use asks in its row as well as in the Sheet");
    const ask = sheetTitled(container, "Move to max-2?");
    assert.equal(
      one(ask, ".sure").textContent,
      "personal and tiltyard move from max-1. Their sessions send their next request from max-2.",
      "the Use Sheet does not name the moving contexts and their source",
    );
    assert.deepEqual(setCalls(fake), [], "asking made a move's call");
    await press(buttonIn(ask, "Move to max-2"));
    await settle(() => container.querySelector(".toast") !== null, "the done toast");
    assert.deepEqual(
      setCalls(fake),
      [
        { context: "personal", account: "max-2" },
        { context: "tiltyard", account: "max-2" },
      ],
      "Move did not make the calls, each moving context to the account",
    );
    assert.equal(
      one(container, ".toast .grow").textContent,
      "Moved personal and tiltyard to max-2.",
      "the done toast does not name the moves",
    );
    assertNone(
      container.querySelector('[role="dialog"][aria-label="Move to max-2?"]'),
      "the Use Sheet is still drawn after its move is done",
    );
  });
});

test("UsageNarrow: a refused move shows its words in the Use Sheet, and not in the accounts", async (t: TestContext) => {
  await withNarrowApp(t, { personal: "max-1", tiltyard: "max-2", deploy: "max-3" }, async ({ container, fake, settle }) => {
    fake.refuseNext("Routes.SetContextAccount", Code.NotFound, "context: unknown id");
    await press(one(container, ".uband"));
    await press(buttonIn(rowOf(container, "max-1"), "Use"));
    await press(buttonIn(sheetTitled(container, "Move to max-1?"), "Move to max-1"));
    await settle(
      () => container.querySelector('[role="dialog"][aria-label="Move to max-1?"] [role="status"]') !== null,
      "the refused move's words",
    );
    assert.equal(
      one(sheetTitled(container, "Move to max-1?"), '[role="status"]').textContent,
      "Not moved: context: unknown id Nothing changed. tiltyard still uses max-2; deploy still uses max-3.",
      "the refusal's words in the Use Sheet do not come from the routes as they stand",
    );
    assertNone(
      container.querySelector('[role="dialog"][aria-label="Claude accounts"]'),
      "the refusal left the accounts Sheet drawn",
    );
    assert.deepEqual(
      setCalls(fake),
      [{ context: "tiltyard", account: "max-1" }],
      "the refused move went on past its refusal",
    );
    assertNone(container.querySelector(".toast"), "a refused move offered a done toast");
  });
});

test("UsageNarrow: the Use Sheet names each moving context's source when they come from more than one account", async (t: TestContext) => {
  await withNarrowApp(t, { personal: "max-1", tiltyard: "max-2", deploy: "max-3", work: "" }, async ({ container }) => {
    await press(one(container, ".uband"));
    await press(buttonIn(rowOf(container, "max-3"), "Use"));
    assert.equal(
      one(sheetTitled(container, "Move to max-3?"), ".sure").textContent,
      "personal from max-1 and tiltyard from max-2 move. Their sessions send their next request from max-3.",
      "the Use Sheet does not name each moving context with its source",
    );
  });
});
