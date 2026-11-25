import assert from "node:assert/strict";
import { type TestContext, test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients } from "./client";
import { accountsServe } from "./fixtures/accounts";
import { type Account, AccountSchema, type RouteRegistry, RouteRegistrySchema } from "./gen/orrery/v1/accounts_pb";
import {
  Usage,
  type UsageReading,
  UsageReadingSchema,
  UsageReadingsResponseSchema,
  type UsageSeatSpend,
  UsageSeatSpendSchema,
  UsageSessionTotalsResponseSchema,
  UsageSpendResponseSchema,
} from "./gen/orrery/v1/usage_pb";
import type { SeenClock } from "./seen";
import { mockTimers } from "./testclock";
import { assertNone, installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until } from "./testwait";
import { createUsageReads } from "./usageload";
import { noUsage, spanSeconds, type UsageData } from "./usagedata";

// wideWidth is the window width the App is drawn at, past the 760px query the inbox's stacked layout follows.
const wideWidth = 1024;
// now is the fake clock's time in unix seconds. Every reset and observed time below is set from it.
const now = 1_800_000_000;
const hour = 3600;
const day = 86400;

// accountList is the one invented account, max-1, which the registry's personal context names.
const accountList: Account[] = [
  create(AccountSchema, {
    id: "max-1",
    name: "max-1",
    provider: "anthropic",
    plan: "Claude Max",
    fingerprint: "fp-max-1",
    created: 1n,
  }),
];

// registry is the invented route registry: personal names max-1 and sends its seats through fwd-seat. The seats are a
// forward seat, a subscription seat, and two metered seats.
const registry: RouteRegistry = create(RouteRegistrySchema, {
  defaultContext: "personal",
  seats: [
    { id: "fwd-seat", authMode: "forward" },
    { id: "sub-seat", authMode: "inject", billing: "subscription" },
    { id: "api-seat", authMode: "inject", billing: "metered" },
    { id: "bedrock-seat", authMode: "sigv4", billing: "metered" },
  ],
  contexts: [{ id: "personal", defaultSeat: "fwd-seat", account: "max-1" }],
});

// readings are the invented limit readings of max-1, both current.
const readings: UsageReading[] = [reading("max-1", "5h", 0.24, now + 2 * hour), reading("max-1", "7d", 0.54, now + 3 * day)];

// reading is one invented limit reading of account for claim, at the share of its limit utilization, resetting at reset.
function reading(account: string, claim: string, utilization: number, reset: number): UsageReading {
  return create(UsageReadingSchema, {
    account,
    claim,
    utilization,
    reset: BigInt(reset),
    status: "allowed",
    observed: BigInt(now - 60),
    dropped: false,
  });
}

// spendOf is one invented seat's spend over the seven days, in nano US dollars: what it billed, and what it lists at.
function spendOf(seat: string, billed: bigint, list: bigint): UsageSeatSpend {
  return create(UsageSeatSpendSchema, { seat, billedCostNanoUsd: billed, listCostNanoUsd: list });
}

// spend is the invented seven days of spend. api-seat billed $44.33 of its $50.00 list price, and bedrock-seat billed and
// listed $3.10. fwd-seat, a forward seat, billed nothing and lists at $1,240. So the band's line reads billed $47.43 and
// list $1,293, and the popover's foot lists api-seat at $44.33 and the subscription traffic at $1,240. Amounts are nano USD,
// so $44.33 is 44330000000.
const spend: UsageSeatSpend[] = [
  spendOf("api-seat", 44_330_000_000n, 50_000_000_000n),
  spendOf("bedrock-seat", 3_100_000_000n, 3_100_000_000n),
  spendOf("fwd-seat", 0n, 1_240_000_000_000n),
];

// subscriptionSpend is the invented seven days of spend with two subscription seats: api-seat as in spend, fwd-seat as
// before, and sub-seat, a subscription seat that billed nothing and lists at $300. Its list price is not billed, so the
// subscription traffic is $1,540, and api-seat's list price is not part of it.
const subscriptionSpend: UsageSeatSpend[] = [
  spendOf("api-seat", 44_330_000_000n, 50_000_000_000n),
  spendOf("fwd-seat", 0n, 1_240_000_000_000n),
  spendOf("sub-seat", 0n, 300_000_000_000n),
];

// usageWith is the usage the App holds once every read has answered, with the spend given, or null for before a Spend read
// lands.
function usageWith(seats: readonly UsageSeatSpend[] | null): UsageData {
  return { ...noUsage, accounts: accountList, registry, readings, spend: seats, answered: true };
}

// fakeTransport serves the accounts and routes, the readings, and the spend. Usage.Spend answers seats for the seven days to
// now, which is the span the reads ask for, and no seats for any other span.
function fakeTransport(seats: readonly UsageSeatSpend[]) {
  const served = accountsServe({ accounts: accountList, registry });
  return createRouterTransport((router) => {
    served.register(router);
    router.service(Usage, {
      readings: () => create(UsageReadingsResponseSchema, { readings }),
      sessionTotals: () => create(UsageSessionTotalsResponseSchema),
      spend: (request) =>
        create(UsageSpendResponseSchema, {
          seats: request.from === BigInt(now - spanSeconds) && request.to === BigInt(now) ? [...seats] : [],
        }),
    });
  });
}

type Clients = ReturnType<typeof createClients>;

// stillClock is a seen clock whose timers never fire, so no row is marked seen.
const stillClock: SeenClock = {
  now: () => now * 1000,
  setTimeout: () => 0,
  clearTimeout: () => {},
};

// Mounted is the App drawn over the fake, with what a test checks: the container it draws into, and draw, which draws the
// App again with the usage given.
interface Mounted {
  container: HTMLElement;
  draw(next: UsageData): Promise<void>;
}

// mountApp draws the App over clients at the wide width, in a fresh DOM, with usage, and runs check. The App is unmounted
// and the DOM removed when check returns or throws.
async function mountApp(
  t: TestContext,
  clients: Clients,
  usage: UsageData,
  check: (mounted: Mounted) => Promise<void>,
): Promise<void> {
  const window = installTestDom();
  window.innerWidth = wideWidth;
  const container = document.createElement("div");
  document.body.append(container);
  const teardown = teardownOnce(t.signal, async () => {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  });
  const draw = async (next: UsageData) => {
    await act(() => {
      render(<App rows={[]} now={now} clients={clients} seenClock={stillClock} usage={next} />, container);
    });
  };
  try {
    await draw(usage);
    await check({ container, draw });
  } finally {
    await teardown();
  }
}

// withApp mounts the App over the fake on the fake clock, with usage, and runs check.
async function withApp(t: TestContext, usage: UsageData, check: (mounted: Mounted) => Promise<void>): Promise<void> {
  mockTimers(t, ["setTimeout", "setInterval"]);
  await mountApp(t, createClients(fakeTransport([])), usage, check);
}

// one returns the element under root that matches selector, which must be drawn.
function one(root: ParentNode, selector: string): HTMLElement {
  const found = root.querySelector<HTMLElement>(selector);
  assert.ok(found !== null, `no element matches ${selector}`);
  return found;
}

// press clicks element, and lets Preact draw what the click changed.
async function press(element: HTMLElement): Promise<void> {
  await act(() => {
    element.dispatchEvent(new Event("click", { bubbles: true, cancelable: true }));
  });
}

// openPopover collapses the inbox with its foot's control, waits for the usage tile the collapsed rail draws, and presses
// the tile. It returns the popover the press opens.
async function openPopover(t: TestContext, container: HTMLElement): Promise<HTMLElement> {
  await press(one(container, '.inbox-foot button[aria-label="Collapse the inbox"]'));
  await until(t.signal, () => container.querySelector(".utile") !== null, "the usage tile", { step: () => act(turn) });
  await press(one(container, ".utile"));
  return one(container, ".upop");
}

// footRows are the popover's foot lines in order, each as its label and its amount.
function footRows(popover: ParentNode): [string, string][] {
  return [...popover.querySelectorAll<HTMLElement>(".up-foot .up-frow")].map((line) => [
    line.querySelector(".up-lbl")?.textContent ?? "",
    line.querySelector(".up-v")?.textContent ?? "",
  ]);
}

test("UsageSpendLine: the band's line under the table bills and lists the seven days' spend over every seat", (t) =>
  withApp(t, usageWith(spend), async ({ container }) => {
    const band = one(container, ".uband");
    const line = one(band, ".uspend");
    assert.equal(line.parentElement, band, "the line is not drawn in the band");
    assert.equal(line.previousElementSibling?.className, "ugrid", "the line is not drawn under the table");
    assert.equal(line.textContent, "Billed 7d $47.43 list $1,293", "the line does not total billed and list over every seat");
  }));

test("UsageSpendLine: the Spend read asks for the seven days to the clock, and the band's line draws its answer", async (t) => {
  mockTimers(t, ["setTimeout", "setInterval"]);
  const clients = createClients(fakeTransport(spend));
  const landed: UsageData[] = [];
  const reads = createUsageReads({
    clients,
    hidden: () => false,
    now: () => now,
    onChange: (data) => landed.push(data),
  });
  reads.tick();
  await until(t.signal, () => landed.length === 1, "the first read to land", { step: turn });
  await mountApp(t, clients, landed[0], async ({ container }) => {
    assert.equal(
      one(container, ".uspend").textContent,
      "Billed 7d $47.43 list $1,293",
      "the line does not draw the Spend answer for the seven days to the clock",
    );
  });
});

test("UsageSpendLine: the band's line and the popover's foot are not drawn until a Spend read lands", (t) =>
  withApp(t, usageWith(null), async ({ container, draw }) => {
    assertNone(container.querySelector(".uspend"), "the band line is drawn before a Spend read lands");
    const popover = await openPopover(t, container);
    assertNone(popover.querySelector(".up-foot"), "the popover foot is drawn before a Spend read lands");
    await draw(usageWith(spend));
    assert.ok(container.querySelector(".uspend") !== null, "the band line is not drawn once a Spend read lands");
    assert.ok(container.querySelector(".upop .up-foot") !== null, "the popover foot is not drawn once a Spend read lands");
  }));

test("UsageSpendLine: the popover's foot lists the billed total, each seat that billed, then the subscription traffic", (t) =>
  withApp(t, usageWith(spend), async ({ container }) => {
    const popover = await openPopover(t, container);
    assert.deepEqual(
      footRows(popover),
      [
        ["billed, last 7 days", "$47.43"],
        ["api-seat", "$44.33"],
        ["bedrock-seat", "$3.10"],
        ["subscription traffic, at list price", "$1,240"],
      ],
      "the foot does not list the billed total, each seat that billed with its amount, and the subscription traffic at list",
    );
  }));

test("UsageSpendLine: forward and subscription-billed seats count as subscription traffic at list, not as billed", (t) =>
  withApp(t, usageWith(subscriptionSpend), async ({ container }) => {
    const popover = await openPopover(t, container);
    assert.deepEqual(
      footRows(popover),
      [
        ["billed, last 7 days", "$44.33"],
        ["api-seat", "$44.33"],
        ["subscription traffic, at list price", "$1,540"],
      ],
      "the forward and subscription seats are not counted as subscription traffic at list",
    );
  }));
