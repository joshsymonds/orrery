import assert from "node:assert/strict";
import { type TestContext, test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { Code, createRouterTransport } from "@connectrpc/connect";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { couldNotTell } from "./accountswrite";
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
import { bandRows } from "./usagemodel";
import { UsagePopover } from "./usagepopover";

// now is the fake clock, in unix seconds. Every time the checks draw is set from it, and no check reads the wall clock.
const now = 1_800_000_000;

// wideWidth is the window width the App is drawn at, past the 760px query the inbox's stacked layout follows.
const wideWidth = 1024;

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

// Drawn is a check's view of what it drew: the fake of the accounts and routes, the DOM the page draws into, and settle, which
// waits until ready holds, letting the page draw between checks.
interface Drawn {
  fake: AccountsServe;
  container: HTMLElement;
  settle(ready: () => boolean, what: string): Promise<void>;
}

// Page is a check's view of the popover: what it drew, and the moves that set every context they planned, with the contexts
// and the account each one set.
interface Page extends Drawn {
  dones: { contexts: string[]; account: string }[];
}

// withPopover draws the popover over a fake seeded with contexts, on a fake clock, and runs check. The popover is unmounted and
// the DOM removed when check returns or throws.
async function withPopover(
  t: TestContext,
  contexts: Readonly<Record<string, string>>,
  check: (page: Page) => Promise<void>,
): Promise<void> {
  mockTimers(t, ["setTimeout", "setInterval"]);
  const window = installTestDom();
  const fake = accountsServe({ accounts, registry: registryOf(contexts) });
  const clients = createClients(createRouterTransport((router) => fake.register(router)));
  const container = document.createElement("div");
  document.body.append(container);
  const dones: { contexts: string[]; account: string }[] = [];
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
        <UsagePopover
          rows={bandRows(usageOf(contexts), now)}
          now={now}
          use={{
            routes: clients.routes,
            registry: registryOf(contexts),
            onDone: (moves, account) => {
              dones.push({ contexts: moves.map((move) => move.context), account });
            },
            onRead: noop,
          }}
          onAccounts={noop}
          onClose={noop}
        />,
        container,
      );
    });
    await check({ fake, container, dones, settle });
  } finally {
    await teardown();
  }
}

// withApp draws the App, wide, over a fake seeded with contexts, on a fake clock, and runs check. The App is unmounted and the
// DOM removed when check returns or throws.
async function withApp(
  t: TestContext,
  contexts: Readonly<Record<string, string>>,
  check: (drawn: Drawn) => Promise<void>,
): Promise<void> {
  mockTimers(t, ["setTimeout", "setInterval"]);
  const window = installTestDom();
  window.innerWidth = wideWidth;
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

// rowOf is the popover's row of the account with id.
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

// ask presses Use in the row of account, which asks the question in that row.
async function ask(container: HTMLElement, account: string): Promise<void> {
  await press(buttonIn(rowOf(container, account), "Use"));
}

// move presses Move in the question the popover asks, which makes the move.
async function move(container: HTMLElement): Promise<void> {
  await press(buttonIn(one(container, ".up-ask"), "Move"));
}

test("UsageUse: a row offers Use when some context names another account, and no Use when every context naming an account names it", async (t: TestContext) => {
  await withPopover(t, { personal: "max-1", tiltyard: "max-1" }, async ({ container }) => {
    assertNone(rowOf(container, "max-1").querySelector(".up-use"), "max-1 offers Use though every context naming an account names it");
    one(rowOf(container, "max-2"), ".up-use");
    one(rowOf(container, "max-3"), ".up-use");
  });
});

test("UsageUse: a row whose account one context names offers Use when another context names a different account", async (t: TestContext) => {
  await withPopover(t, { personal: "max-1", tiltyard: "max-2" }, async ({ container }) => {
    one(rowOf(container, "max-1"), ".up-use");
    assertNone(rowOf(container, "max-1").querySelector(".up-inuse"), "max-1 reads in use though tiltyard names max-2");
  });
});

test("UsageUse: a row every account-naming context names reads in use, and offers no Use", async (t: TestContext) => {
  await withPopover(t, { personal: "max-1", tiltyard: "max-1", work: "" }, async ({ container }) => {
    assert.equal(one(rowOf(container, "max-1"), ".up-inuse").textContent, "in use", "max-1 does not read in use");
    assertNone(rowOf(container, "max-1").querySelector(".up-use"), "max-1 reads in use and still offers Use");
    one(rowOf(container, "max-2"), ".up-use");
  });
});

test("UsageUse: no row offers Use or reads in use when no context names an account", async (t: TestContext) => {
  await withPopover(t, { personal: "", work: "" }, async ({ container }) => {
    assert.equal(container.querySelectorAll(".up-row").length, accounts.length, "the popover does not draw a row for each account");
    assertNone(container.querySelector(".up-use"), "a row offers Use with no context naming an account");
    assertNone(container.querySelector(".up-inuse"), "a row reads in use with no context naming an account");
  });
});

test("UsageUse: the question names every moving context, with its source when they come from more than one account", async (t: TestContext) => {
  await withPopover(t, { personal: "max-1", tiltyard: "max-2", deploy: "max-3", work: "" }, async ({ container }) => {
    await ask(container, "max-2");
    assert.equal(
      one(rowOf(container, "max-2"), ".up-q").textContent,
      "Move personal from max-1 and deploy from max-3 to max-2? Their sessions send their next request from it.",
      "the question does not name each moving context and its source",
    );
  });
});

test("UsageUse: the question names the contexts alone when they all come from one account", async (t: TestContext) => {
  await withPopover(t, { personal: "max-1", tiltyard: "max-1" }, async ({ container }) => {
    await ask(container, "max-3");
    assert.equal(
      one(rowOf(container, "max-3"), ".up-q").textContent,
      "Move personal and tiltyard to max-3? Their sessions send their next request from it.",
      "the question does not name the moving contexts",
    );
  });
});

test("UsageUse: Cancel takes the question away and makes no call", async (t: TestContext) => {
  await withPopover(t, { personal: "max-1", tiltyard: "max-1" }, async ({ container, fake }) => {
    await ask(container, "max-2");
    await press(buttonIn(one(container, ".up-ask"), "Cancel"));
    assertNone(container.querySelector(".up-ask"), "Cancel leaves the question drawn");
    assert.equal(fake.calls.length, 0, "Cancel made a call");
  });
});

test("UsageUse: Move sets each moving context to the account in order, and reports the contexts it set", async (t: TestContext) => {
  await withPopover(
    t,
    { personal: "max-1", work: "", tiltyard: "max-3", deploy: "max-2" },
    async ({ container, fake, dones, settle }) => {
      await ask(container, "max-2");
      await move(container);
      await settle(() => dones.length === 1, "the move's done report");
      assert.deepEqual(
        setCalls(fake),
        [
          { context: "personal", account: "max-2" },
          { context: "tiltyard", account: "max-2" },
        ],
        "Move did not set each moving context to the account, in order",
      );
      assert.deepEqual(dones, [{ contexts: ["personal", "tiltyard"], account: "max-2" }], "the done report names the wrong moves");
    },
  );
});

test("UsageUse: a refused move says in its row what did not move, from a Routes.Get read, and stops at the refusal", async (t: TestContext) => {
  await withPopover(t, { personal: "max-1", tiltyard: "max-2", deploy: "max-3" }, async ({ container, fake, dones, settle }) => {
    fake.refuseNext("Routes.SetContextAccount", Code.NotFound, "context: unknown id");
    await ask(container, "max-1");
    await move(container);
    await settle(() => container.querySelector(".up-note") !== null, "the refused move's words");
    assert.equal(
      one(rowOf(container, "max-1"), ".up-note").textContent,
      "Not moved: context: unknown id Nothing changed. tiltyard still uses max-2; deploy still uses max-3.",
      "the refusal's words do not come from the routes as they stand",
    );
    assert.deepEqual(
      fake.calls.map((call) => call.method),
      ["Routes.SetContextAccount", "Routes.Get"],
      "the move did not read the routes once after its refused call, or went on past the refusal",
    );
    assert.deepEqual(dones, [], "a refused move reported done");
  });
});

test("UsageUse: when the Routes.Get read after a refusal fails, the row says it could not tell", async (t: TestContext) => {
  await withPopover(t, { personal: "max-1", tiltyard: "max-2", deploy: "max-3" }, async ({ container, fake, settle }) => {
    fake.refuseNext("Routes.SetContextAccount", Code.NotFound, "context: unknown id");
    fake.refuseNext("Routes.Get", Code.Unavailable, "routes: no answer");
    await ask(container, "max-1");
    await move(container);
    await settle(() => container.querySelector(".up-note") !== null, "the move's words");
    assert.equal(one(rowOf(container, "max-1"), ".up-note").textContent, couldNotTell, "the row does not say it could not tell");
  });
});

test("UsageUse: a done move shows its toast, and Undo moves each context back to the account it was on", async (t: TestContext) => {
  await withApp(t, { personal: "max-1", work: "", tiltyard: "max-3", deploy: "max-2" }, async ({ fake, container, settle }) => {
    await press(one(container, ".uband"));
    await ask(container, "max-2");
    await move(container);
    await settle(() => container.querySelector(".toast") !== null, "the done toast");
    assert.equal(one(container, ".toast .grow").textContent, "Moved personal and tiltyard to max-2.", "the toast does not name the moves");
    assertNone(container.querySelector(".upop"), "the popover stays open after its move is done");
    await press(buttonIn(one(container, ".toast"), "Undo"));
    await settle(() => setCalls(fake).length === 4, "the calls Undo makes");
    assertNone(container.querySelector(".toast"), "Undo left the toast drawn");
    assert.deepEqual(
      setCalls(fake),
      [
        { context: "personal", account: "max-2" },
        { context: "tiltyard", account: "max-2" },
        { context: "personal", account: "max-1" },
        { context: "tiltyard", account: "max-3" },
      ],
      "Undo did not restore each moved context's own account",
    );
  });
});

test("UsageUse: an Undo that is refused says in the toast what stands, and offers no Undo of its own", async (t: TestContext) => {
  await withApp(t, { personal: "max-1", work: "", tiltyard: "max-3", deploy: "max-2" }, async ({ fake, container, settle }) => {
    await press(one(container, ".uband"));
    await ask(container, "max-2");
    await move(container);
    await settle(() => container.querySelector(".toast") !== null, "the done toast");
    fake.refuseNext("Routes.SetContextAccount", Code.NotFound, "context: unknown id");
    await press(buttonIn(one(container, ".toast"), "Undo"));
    await settle(
      () => container.querySelector(".toast .grow")?.textContent?.startsWith("Not moved back") === true,
      "the refused Undo's words",
    );
    assert.equal(
      one(container, ".toast .grow").textContent,
      "Not moved back: context: unknown id Nothing changed. personal still uses max-2; tiltyard still uses max-2.",
      "the refused Undo does not say what stands",
    );
    assertNone(container.querySelector(".toast button"), "a refused Undo offers another Undo");
  });
});
