import assert from "node:assert/strict";
import { test, type TestContext } from "node:test";

import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { type JSX, render } from "preact";
import { useState } from "preact/hooks";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients } from "./client";
import { accountsServe } from "./fixtures/accounts";
import { usageServe } from "./fixtures/usage";
import { type Account, AccountSchema, RouteContextSchema, RouteRegistrySchema } from "./gen/orrery/v1/accounts_pb";
import { UsageReadingSchema } from "./gen/orrery/v1/usage_pb";
import { InboxRail } from "./inboxrail";
import type { SeenClock } from "./seen";
import { mockTimers } from "./testclock";
import { installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until } from "./testwait";
import type { UsageData } from "./usagedata";
import { type BandRow, bandRows, firstInUse } from "./usagemodel";
import { UsagePopover } from "./usagepopover";
import { UsageRailTile } from "./usagerail";

// withDom gives the check a container on the document, and removes the container and the DOM however the check ends.
async function withDom(check: (container: HTMLElement) => void): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.appendChild(container);
  try {
    check(container);
  } finally {
    render(null, container);
    container.remove();
    await removeTestDom(window);
  }
}

// draw renders the element into the container, and flushes its effects.
function draw(container: HTMLElement, element: JSX.Element): void {
  act(() => {
    render(element, container);
  });
}

// one returns the first element under root that matches selector, which must be there.
function one(root: ParentNode, selector: string): HTMLElement {
  const found = root.querySelector<HTMLElement>(selector);
  assert.ok(found !== null, `no element matches ${selector}`);
  return found;
}

// assertShare checks that a ring's arc covers share of its circle: the first length of its dash array is share of the circumference.
function assertShare(arc: Element, share: number, radius: number): void {
  const dash = arc.getAttribute("stroke-dasharray") ?? "";
  const drawn = Number.parseFloat(dash.split(" ")[0]);
  assert.ok(Math.abs(drawn - share * 2 * Math.PI * radius) < 0.05, `the arc of r ${radius} draws ${dash}, not ${share} of its circle`);
}

// The clock of the checks, in unix seconds. Every observed time and reset below is set from it, so no check reads the wall clock.
const now = 1_800_000_000;
const hour = 3600;
const day = 86400;

const noop = () => undefined;

// wideWidth is the window width the App is drawn at: wider than the 760px query the collapsed rail follows.
const wideWidth = 1024;

// stillClock is a seen clock whose timers never fire, so no row of the App is marked seen.
const stillClock: SeenClock = {
  now: () => now * 1000,
  setTimeout: () => 0,
  clearTimeout: () => {},
};

// account makes an invented account.
function account(id: string): Account {
  return create(AccountSchema, { id, name: id, provider: "claude" });
}

// reading makes one invented limit reading of an account's claim, observed a minute before now.
function reading(id: string, claim: string, utilization: number, status: string, reset: number) {
  return create(UsageReadingSchema, {
    account: id,
    claim,
    utilization,
    reset: BigInt(reset),
    status,
    observed: BigInt(now - 60),
    dropped: false,
  });
}

// dataOf is the usage the checks draw: the accounts and readings given, and the contexts, each naming an account or none.
function dataOf(accounts: Account[], contexts: [string, string][], readings: ReturnType<typeof reading>[]): UsageData {
  return {
    accounts,
    registry: create(RouteRegistrySchema, {
      contexts: contexts.map(([id, owner]) => create(RouteContextSchema, { id, account: owner })),
    }),
    readings,
    spend: null,
    answered: true,
    failed: false,
  };
}

// rowOf is the first row in use of the data, the row the rail's tile draws; null when no account is in use.
function rowOf(data: UsageData): BandRow | null {
  return firstInUse(bandRows(data, now));
}

// rowInUse is the first row in use of the data, which the checks that need a row name.
function rowInUse(data: UsageData): BandRow {
  const row = rowOf(data);
  assert.ok(row !== null, "the data names no account in use");
  return row;
}

// twoAccounts are max-1 and max-2, in that order, and twoReadings are their windows: max-1's 5h is at 66 percent, warn, and its
// 7d at 83, crit; max-2's 5h and 7d are at 10 and 20 percent, both ok.
const twoAccounts = [account("max-1"), account("max-2")];
const twoReadings = [
  reading("max-1", "5h", 0.66, "allowed", now + hour),
  reading("max-1", "7d", 0.83, "allowed", now + 2 * day),
  reading("max-2", "5h", 0.1, "allowed", now + hour),
  reading("max-2", "7d", 0.2, "allowed", now + 2 * day),
];

// twoRegistry has personal on max-1 and tiltyard on max-2, so both accounts are in use and max-1, first in the accounts'
// order, is the first in use.
const twoRegistry = create(RouteRegistrySchema, {
  contexts: [
    create(RouteContextSchema, { id: "personal", account: "max-1" }),
    create(RouteContextSchema, { id: "tiltyard", account: "max-2" }),
  ],
});

// two is the data of the two accounts, both in use.
const two: UsageData = {
  accounts: twoAccounts,
  registry: twoRegistry,
  readings: twoReadings,
  spend: null,
  answered: true,
  failed: false,
};

// Rail is the collapsed inbox's rail with no project tiles and no flyout, and the given usage tile, as app draws it.
function Rail({ usage }: { usage?: { row: BandRow; onOpen: () => void; popover?: JSX.Element } }): JSX.Element {
  return (
    <InboxRail
      tiles={[]}
      flyout={null}
      now={now}
      onMark={noop}
      onTile={noop}
      onHover={noop}
      onHoverOff={noop}
      onFlyoutRow={noop}
      onFlyoutPage={noop}
      usage={usage}
      onNew={noop}
      onSearch={noop}
      unread={0}
      onNotices={noop}
      onKeys={noop}
    />
  );
}

// Harness draws the rail as app draws it: pressing the usage tile opens the popover of its row beside the rail.
function Harness({ row }: { row: BandRow }): JSX.Element {
  const [open, setOpen] = useState(false);
  return (
    <Rail usage={{ row, onOpen: () => setOpen(true), popover: open ? <UsagePopover row={row} now={now} /> : undefined }} />
  );
}

test("UsageRail: the usage tile is drawn directly above New on the collapsed rail", async () => {
  await withDom((container) => {
    draw(container, <Rail usage={{ row: rowInUse(two), onOpen: noop }} />);
    const tile = one(container, ".utile");
    assert.equal(tile.parentElement?.getAttribute("aria-label"), "inbox, collapsed", "the tile is not on the rail");
    assert.equal(tile.nextElementSibling?.getAttribute("aria-label"), "New session", "the tile is not directly above New");
  });
});

test("UsageRail: two rings for the first account in use, 7d outside and 5h inside, each in its level", async () => {
  await withDom((container) => {
    draw(container, <Rail usage={{ row: rowInUse(two), onOpen: noop }} />);
    const tile = one(container, ".utile");
    assert.equal(tile.querySelectorAll("circle[data-claim]").length, 2, "the tile does not draw exactly two rings");
    const outer = one(tile, 'circle[data-claim="7d"]');
    assert.equal(outer.getAttribute("r"), "12", "the 7d ring is not outside");
    assert.ok(outer.classList.contains("level-crit"), "the 7d ring is not in the crit level");
    assertShare(outer, 0.83, 12);
    const inner = one(tile, 'circle[data-claim="5h"]');
    assert.equal(inner.getAttribute("r"), "7", "the 5h ring is not inside");
    assert.ok(inner.classList.contains("level-warn"), "the 5h ring is not in the warn level");
    assertShare(inner, 0.66, 7);
  });
});

test("UsageRail: a rejected account draws both its rings crit, whatever their percent", async () => {
  const limited = dataOf(
    [account("max-3")],
    [["work", "max-3"]],
    [reading("max-3", "5h", 1, "rejected", now + hour), reading("max-3", "7d", 0.2, "allowed", now + day)],
  );
  await withDom((container) => {
    draw(container, <Rail usage={{ row: rowInUse(limited), onOpen: noop }} />);
    const tile = one(container, ".utile");
    assert.ok(one(tile, 'circle[data-claim="7d"]').classList.contains("level-crit"), "the 7d ring of a rejected account is not crit");
    assert.ok(one(tile, 'circle[data-claim="5h"]').classList.contains("level-crit"), "the 5h ring of a rejected account is not crit");
  });
});

test("UsageRail: the tip names the account and both values", async () => {
  await withDom((container) => {
    draw(container, <Rail usage={{ row: rowInUse(two), onOpen: noop }} />);
    const tile = one(container, ".utile");
    assert.equal(tile.getAttribute("title"), "max-1 · 5h 66% · 7d 83%");
    assert.equal(tile.getAttribute("aria-label"), "max-1 · 5h 66% · 7d 83%");
  });
});

test("UsageRail: a window no current reading carries draws its ring as the track alone, and the tip says no reading", async () => {
  const bare = dataOf([account("pro-1")], [["notes", "pro-1"]], []);
  await withDom((container) => {
    draw(container, <Rail usage={{ row: rowInUse(bare), onOpen: noop }} />);
    const tile = one(container, ".utile");
    assert.equal(tile.querySelectorAll("circle[data-claim]").length, 0, "a ring without a reading drew an arc");
    assert.equal(tile.getAttribute("title"), "pro-1 · 5h no reading · 7d no reading");
  });
});

test("UsageRail: no tile is drawn when no account is in use", async () => {
  const unused = dataOf([account("max-1")], [["personal", ""]], [reading("max-1", "5h", 0.2, "allowed", now + hour)]);
  await withDom((container) => {
    const row = rowOf(unused);
    draw(container, <Rail usage={row === null ? undefined : { row, onOpen: noop }} />);
    assert.equal(container.querySelector(".utile"), null, "a tile is drawn with no account in use");
    assert.ok(container.querySelector('button[aria-label="New session"]') !== null, "the rail does not draw New");
  });
});

test("UsageRail: a click on the usage tile calls onOpen", async () => {
  await withDom((container) => {
    let opened = 0;
    draw(container, <UsageRailTile row={rowInUse(two)} onOpen={() => (opened += 1)} />);
    one(container, ".utile").click();
    assert.equal(opened, 1, "the click did not call onOpen once");
  });
});

test("UsageRail: a click opens the popover beside the rail", async () => {
  await withDom((container) => {
    draw(container, <Harness row={rowInUse(two)} />);
    assert.equal(container.querySelector(".upop"), null, "the popover is open before the tile is pressed");
    act(() => {
      one(container, ".utile").click();
    });
    const nav = one(container, 'nav[aria-label="inbox, collapsed"]');
    const popover = one(nav, ".upop");
    assert.equal(popover.closest("nav"), nav, "the popover is not beside the rail");
    assert.equal(one(popover, ".up-nm").textContent, "max-1", "the popover does not name the account");
    assert.equal(one(popover, ".up-win.level-warn .up-v").textContent, "66%", "the popover does not show the 5h value");
  });
});

test("UsageRail: the popover counts each window's reset down from the clock, and a window with no reading says so", async () => {
  await withDom((container) => {
    const row = rowInUse(two);
    draw(container, <UsagePopover row={row} now={now} />);
    const wins = Array.from(container.querySelectorAll(".up-win"));
    assert.equal(wins.length, 3, "the popover does not draw each window");
    assert.equal(wins[0].querySelector(".up-r")?.textContent, "in 1h", "the 5h reset is not counted from now");
    assert.equal(wins[1].querySelector(".up-r")?.textContent, "in 2d", "the 7d reset is not counted from now");
    assert.equal(wins[2].querySelector(".up-v")?.textContent, "no reading", "the Fable window with no reading does not say so");
  });
});

// press clicks the element, and lets Preact draw what the click changed.
async function press(element: Element): Promise<void> {
  await act(() => {
    element.dispatchEvent(new Event("click", { bubbles: true, cancelable: true }));
  });
}

test("UsageRail: the app reads the usage its collapsed rail draws, and a press on the tile opens the popover beside the rail", async (t: TestContext) => {
  mockTimers(t, ["setTimeout", "setInterval"]);
  const window = installTestDom();
  window.innerWidth = wideWidth;
  const accounts = accountsServe({ accounts: twoAccounts, registry: twoRegistry });
  const usage = usageServe({ readings: twoReadings, spend: [] });
  const clients = createClients(
    createRouterTransport((router) => {
      accounts.register(router);
      usage.register(router);
    }),
  );
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
      render(<App rows={[]} now={now} clients={clients} seenClock={stillClock} />, container);
    });
    await press(one(container, '.inbox-foot button[aria-label="Collapse the inbox"]'));
    await settle(() => container.querySelector(".utile") !== null, "the usage tile on the collapsed rail");
    const tile = one(container, ".utile");
    assert.equal(tile.nextElementSibling?.getAttribute("aria-label"), "New session", "the app does not draw the tile directly above New");
    assert.equal(tile.getAttribute("title"), "max-1 · 5h 66% · 7d 83%", "the app's tile does not name the account in use and its values");
    await press(tile);
    const popover = one(container, 'nav[aria-label="inbox, collapsed"] .upop');
    assert.equal(one(popover, ".up-nm").textContent, "max-1", "the press did not open the popover of the account in use");
  } finally {
    await teardown();
  }
});
