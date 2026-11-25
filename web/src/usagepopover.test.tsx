import assert from "node:assert/strict";
import { type TestContext, test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { type JSX, render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients } from "./client";
import { accountsServe } from "./fixtures/accounts";
import { type Account, AccountSchema, RouteContextSchema, RouteRegistrySchema } from "./gen/orrery/v1/accounts_pb";
import { type UsageReading, UsageReadingSchema } from "./gen/orrery/v1/usage_pb";
import { formatAddress } from "./router";
import type { SeenClock } from "./seen";
import { mockTimers } from "./testclock";
import { assertNone, installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until } from "./testwait";
import { noUsage, type UsageData } from "./usagedata";
import { type BandRow, bandRows } from "./usagemodel";
import { UsagePopover } from "./usagepopover";

// The clock of the checks, in unix seconds. Every observed time and reset below is set from it, so no check reads the wall clock.
const now = 1_800_000_000;
const hour = 3600;
const day = 86400;

// wideWidth is the window width the App is drawn at, past the 760px query the inbox's stacked layout follows.
const wideWidth = 1024;

const noop = () => undefined;

// stillClock is a seen clock whose timers never fire, so no row of the App is marked seen.
const stillClock: SeenClock = {
  now: () => now * 1000,
  setTimeout: () => 0,
  clearTimeout: () => {},
};

// account makes an invented account of the plan given.
function account(id: string, plan: string): Account {
  return create(AccountSchema, { id, name: id, provider: "claude", plan });
}

// reading makes one invented limit reading of an account's claim, which resets at reset and was observed at observed.
function reading(id: string, claim: string, utilization: number, status: string, reset: number, observed = now - 60): UsageReading {
  return create(UsageReadingSchema, {
    account: id,
    claim,
    utilization,
    reset: BigInt(reset),
    status,
    observed: BigInt(observed),
    dropped: false,
  });
}

// The invented accounts: max-1 is in use by personal and tiltyard, max-2 and max-3 are not, max-3 is limited, and pro-1 was
// read two hours ago, so it is stale. max-2 has no Fable reading.
const accounts: Account[] = [
  account("max-1", "Claude Max"),
  account("max-2", "Claude Max"),
  account("max-3", "Claude Max"),
  account("pro-1", "Claude Pro"),
];

const registry = create(RouteRegistrySchema, {
  contexts: [
    create(RouteContextSchema, { id: "personal", account: "max-1" }),
    create(RouteContextSchema, { id: "tiltyard", account: "max-1" }),
  ],
});

const readings: UsageReading[] = [
  reading("max-1", "5h", 0.24, "allowed", now + 2 * hour + 20 * 60),
  reading("max-1", "7d", 0.54, "allowed", now + 3 * day),
  reading("max-1", "7d_oi", 0.31, "allowed", now + 3 * day),
  reading("max-2", "5h", 0.66, "allowed", now + hour),
  reading("max-2", "7d", 0.83, "allowed", now + 2 * day),
  reading("max-3", "5h", 1, "rejected", now + 70 * 60),
  reading("max-3", "7d", 0.96, "allowed", now + day),
  reading("pro-1", "5h", 0.03, "allowed", now + 4 * hour + 40 * 60, now - 2 * hour),
];

// usage is the invented usage every check draws, answered, with the accounts above.
const usage: UsageData = { ...noUsage, accounts, registry, readings, answered: true };

// rows are the band's rows of the usage, at now.
const rows: BandRow[] = bandRows(usage, now);

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

// rowOf is the popover's row of the account named.
function rowOf(root: ParentNode, id: string): HTMLElement {
  return one(root, `.up-row[data-account="${id}"]`);
}

// clockOf is the clock time of at in the viewer's zone, as the popover writes it: two digits each side of the colon.
function clockOf(at: number): string {
  const when = new Date(at * 1000);
  return `${String(when.getHours()).padStart(2, "0")}:${String(when.getMinutes()).padStart(2, "0")}`;
}

// press clicks the element, and lets Preact draw what the click changed.
async function press(element: Element): Promise<void> {
  await act(() => {
    element.dispatchEvent(new Event("click", { bubbles: true, cancelable: true }));
  });
}

test("UsagePopover: a click on the band opens the popover beside the band", async (t: TestContext) => {
  mockTimers(t, ["setTimeout", "setInterval"]);
  const window = installTestDom();
  window.innerWidth = wideWidth;
  const fake = accountsServe({ accounts, registry });
  const clients = createClients(createRouterTransport((router) => fake.register(router)));
  const reports: { form: string; mode: "push" | "replace" }[] = [];
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
          usage={usage}
          onAddress={(address, mode) => reports.push({ form: formatAddress(address), mode })}
        />,
        container,
      );
    });
    const band = one(container, ".uband");
    assertNone(band.querySelector(".upop"), "the popover is open before the band is pressed");
    await press(band);
    const popover = one(band, ".upop");
    assert.equal(popover.parentElement, band, "the popover does not lie beside the band");
    assert.equal(one(popover, ".up-nm").textContent, "max-1", "the popover does not draw the accounts of the band");
    assert.equal(
      container.querySelectorAll(".upop .up-row").length,
      rows.length,
      "the popover does not draw a row for each account of the band",
    );
    await press(one(popover, ".up-link"));
    await settle(() => reports.some((report) => report.form === "#/accounts"), "the accounts address");
    assert.ok(
      reports.some((report) => report.form === "#/accounts" && report.mode === "push"),
      "the link did not push #/accounts",
    );
    assertNone(container.querySelector(".upop"), "the popover is still open once the accounts page opens");
  } finally {
    await teardown();
  }
});

test("UsagePopover: each window draws its value, its bar and its countdown, with its clock in the tip", async () => {
  await withDom((container) => {
    draw(container, <UsagePopover rows={rows} now={now} onAccounts={noop} onClose={noop} />);
    const windows = [...rowOf(container, "max-1").querySelectorAll<HTMLElement>(".up-win")];
    assert.deepEqual(
      windows.map((cell) => one(cell, ".up-v").textContent),
      ["24%", "54%", "31%"],
      "the windows do not draw their values",
    );
    assert.deepEqual(
      windows.map((cell) => one(cell, ".up-bar i").style.width),
      ["24%", "54%", "31%"],
      "the windows do not draw their bars",
    );
    assert.deepEqual(
      windows.map((cell) => one(cell, ".up-r").textContent),
      ["in 2h 20m", "in 3d", "in 3d"],
      "the windows do not count down to their resets",
    );
    assert.ok(windows[0].getAttribute("title")?.startsWith("resets "), "the 5h tip does not name the reset");
    assert.ok(windows[0].getAttribute("title")?.endsWith(clockOf(now + 2 * hour + 20 * 60)), "the 5h tip is not the reset's clock");
    assert.ok(windows[1].getAttribute("title")?.endsWith(clockOf(now + 3 * day)), "the 7d tip is not the reset's clock");
  });
});

test("UsagePopover: a window no current reading carries draws no bar and no countdown", async () => {
  await withDom((container) => {
    draw(container, <UsagePopover rows={rows} now={now} onAccounts={noop} onClose={noop} />);
    const fable = [...rowOf(container, "max-2").querySelectorAll<HTMLElement>(".up-win")][2];
    assert.equal(one(fable, ".up-v").textContent, "no reading", "the window with no reading does not say so");
    assertNone(fable.querySelector(".up-bar"), "the window with no reading draws a bar");
    assertNone(fable.querySelector(".up-r"), "the window with no reading counts down");
  });
});

test("UsagePopover: under each name, who uses the account, else its plan, or when its limit lifts", async () => {
  await withDom((container) => {
    draw(container, <UsagePopover rows={rows} now={now} onAccounts={noop} onClose={noop} />);
    assert.equal(one(rowOf(container, "max-1"), ".up-who").textContent, "used by personal, tiltyard", "who uses max-1 is not named");
    assert.equal(one(rowOf(container, "max-2"), ".up-who").textContent, "Claude Max", "a row not in use does not name its plan");
    assert.equal(
      one(rowOf(container, "max-3"), ".up-who").textContent,
      `limited until ${clockOf(now + 70 * 60)}`,
      "a limited row does not name when its limit lifts",
    );
    assert.equal(one(rowOf(container, "pro-1"), ".up-who").textContent, "Claude Pro", "the plan of pro-1 is not named");
  });
});

test("UsagePopover: under each name, read and the age of its reading, stale when it is over an hour old", async () => {
  await withDom((container) => {
    draw(container, <UsagePopover rows={rows} now={now} onAccounts={noop} onClose={noop} />);
    assert.equal(one(rowOf(container, "max-1"), ".up-read").textContent, "read 1m ago", "max-1 does not say when it was read");
    assert.ok(!one(rowOf(container, "max-1"), ".up-read").classList.contains("stale"), "a fresh reading is drawn stale");
    const stale = one(rowOf(container, "pro-1"), ".up-read");
    assert.equal(stale.textContent, "read 2h ago", "pro-1 does not say how old its reading is");
    assert.ok(stale.classList.contains("stale"), "a reading two hours old is not drawn stale");
  });
});

test("UsagePopover: the Accounts and routes link points at #/accounts and calls onAccounts", async () => {
  await withDom((container) => {
    let opened = 0;
    draw(container, <UsagePopover rows={rows} now={now} onAccounts={() => (opened += 1)} onClose={noop} />);
    const link = one(container, ".up-link");
    assert.equal(link.textContent, "Accounts and routes");
    assert.equal(link.getAttribute("href"), "#/accounts");
    link.dispatchEvent(new Event("click", { bubbles: true, cancelable: true }));
    assert.equal(opened, 1, "the link did not call onAccounts once");
  });
});

test("UsagePopover: Escape and a press outside it call onClose, and a press inside it does not", async () => {
  await withDom((container) => {
    let closed = 0;
    draw(container, <UsagePopover rows={rows} now={now} onAccounts={noop} onClose={() => (closed += 1)} />);
    one(container, ".up-head").dispatchEvent(new Event("pointerdown", { bubbles: true }));
    assert.equal(closed, 0, "a press inside the popover closed it");
    document.body.dispatchEvent(new Event("pointerdown", { bubbles: true }));
    assert.equal(closed, 1, "a press outside the popover did not close it");
    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
    assert.equal(closed, 2, "Escape did not close the popover");
    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter" }));
    assert.equal(closed, 2, "a key other than Escape closed the popover");
  });
});
