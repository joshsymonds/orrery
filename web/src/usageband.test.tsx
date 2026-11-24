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
import { type UsageReading, UsageReadingSchema } from "./gen/orrery/v1/usage_pb";
import { formatAddress } from "./router";
import type { SeenClock } from "./seen";
import { mockTimers } from "./testclock";
import { installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until } from "./testwait";
import { noUsage, type UsageData } from "./usagedata";

// wideWidth is the window width the band is drawn at, past the 760px query the inbox's stacked layout follows.
const wideWidth = 1024;
// now is the fake clock's time in unix seconds. Every reset and observed time below is set from it.
const now = 1_800_000_000;
const hour = 3600;
const day = 86400;

// account is one invented account, of the plan given.
function account(id: string, plan: string): Account {
  return create(AccountSchema, { id, name: id, provider: "anthropic", plan, fingerprint: `fp-${id}`, created: 1n });
}

// accounts are the four invented accounts, in the order Accounts.List answers them. The order is not alphabetical, so the
// band's rows show that they keep it.
const accounts: Account[] = [
  account("moss-2", "Claude Max"),
  account("ember-1", "Claude Max"),
  account("vale-4", "Claude Max"),
  account("tide-3", "Claude Pro"),
];

// registry is the invented route registry: ember-1 is named by a context, tide-3 by another, and work names no account.
const registry: RouteRegistry = create(RouteRegistrySchema, {
  defaultContext: "ember-ctx",
  contexts: [
    { id: "ember-ctx", defaultSeat: "anthropic", account: "ember-1" },
    { id: "tide-ctx", defaultSeat: "anthropic", account: "tide-3" },
    { id: "work-ctx", defaultSeat: "anthropic", account: "" },
  ],
});

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

// readings are the invented limit readings. ember-1 sits at the warn edge, 60, and at 79, 59 under it; moss-2 at the crit
// edge, 80, with no Fable reading; tide-3 has no 5h reading; vale-4's only reading reset before now, so it is not current.
const readings: UsageReading[] = [
  reading("ember-1", "5h", 0.6, now + 2 * hour),
  reading("ember-1", "7d", 0.79, now + 3 * day),
  reading("ember-1", "7d_oi", 0.59, now + 3 * day),
  reading("moss-2", "5h", 0.8, now + hour),
  reading("moss-2", "7d", 0.4, now + 2 * day),
  reading("tide-3", "7d", 0.22, now + 12 * hour),
  reading("tide-3", "7d_oi", 0.9, now + 12 * hour),
  reading("vale-4", "5h", 0.3, now - 60),
];

// usage is the usage the page holds once every read has answered.
const usage: UsageData = { ...noUsage, accounts, registry, readings, answered: true };

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

// Mounted is the App drawn over the fake, with what a test checks: the container it draws into, the addresses App
// reported, settle, which waits until ready holds, letting the App draw between checks, and draw, which draws the App again
// with the usage given.
interface Mounted {
  container: HTMLElement;
  reports: Report[];
  settle(ready: () => boolean, what: string): Promise<void>;
  draw(next: UsageData): Promise<void>;
}

// withApp mounts the App over the fixture accounts, with usage, in a fresh DOM at the wide width, with its timers on the
// fake clock, and runs check. The App is unmounted and the DOM removed when check returns or throws.
async function withApp(t: TestContext, next: UsageData, check: (mounted: Mounted) => Promise<void>): Promise<void> {
  mockTimers(t, ["setTimeout", "setInterval"]);
  const window = installTestDom();
  window.innerWidth = wideWidth;
  const fake = accountsServe({ accounts, registry });
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
  const draw = async (usageDrawn: UsageData) => {
    await act(() => {
      render(
        <App
          rows={[]}
          now={now}
          clients={clients}
          seenClock={stillClock}
          usage={usageDrawn}
          onAddress={(address, mode) => reports.push({ form: formatAddress(address), mode })}
        />,
        container,
      );
    });
  };
  try {
    await draw(next);
    await check({ container, reports, settle, draw });
  } finally {
    await teardown();
  }
}

// band is the usage band the App draws, which must be drawn.
function band(container: ParentNode): HTMLElement {
  const drawn = container.querySelector<HTMLElement>(".uband");
  assert.ok(drawn !== null, "the usage band is not drawn");
  return drawn;
}

// rowsOf is the band's rows, in the order the band draws them.
function rowsOf(container: ParentNode): HTMLElement[] {
  return [...band(container).querySelectorAll<HTMLElement>(".urow")];
}

// cellsOf is a row's cells in column order: the name, the 5h, 7d and Fable windows, then the reset.
function cellsOf(row: HTMLElement): HTMLElement[] {
  return [...row.children] as HTMLElement[];
}

// nameOf is the account a row draws.
function nameOf(row: HTMLElement): string {
  return row.querySelector(".uname .t")?.textContent ?? "";
}

// valueOf is a window cell's value, or "" when the cell is empty.
function valueOf(cell: HTMLElement): string {
  return cell.querySelector(".v")?.textContent ?? "";
}

// press clicks element, and lets Preact draw what the click changed.
async function press(element: HTMLElement): Promise<void> {
  await act(() => {
    element.dispatchEvent(new Event("click", { bubbles: true, cancelable: true }));
  });
}

test("UsageBand: drawing the band under the inbox head, its rows in Accounts.List order", (t) =>
  withApp(t, usage, async ({ container }) => {
    const head = container.querySelector(".inbox-head");
    assert.ok(head !== null, "the inbox head is not drawn");
    assert.equal(head.nextElementSibling?.getAttribute("class"), "uband", "the band is not drawn under the inbox head");
    assert.deepEqual(rowsOf(container).map(nameOf), ["moss-2", "ember-1", "vale-4", "tide-3"]);
  }));

test("UsageBand: drawing the 5h, 7d and Fable columns, with an empty window where no current reading carries it", (t) =>
  withApp(t, usage, async ({ container }) => {
    const heads = [...band(container).querySelectorAll(".ugrid > .hd")].map((head) => head.textContent);
    assert.deepEqual(heads, ["Claude", "5h", "7d", "Fable", "resets"]);
    const windows = rowsOf(container).map((row) => cellsOf(row).slice(1, 4));
    assert.deepEqual(
      windows.map((cells) => cells.map(valueOf)),
      [
        ["80%", "40%", ""],
        ["60%", "79%", "59%"],
        ["", "", ""],
        ["", "22%", "90%"],
      ],
    );
    for (const cell of windows.flat().filter((cell) => valueOf(cell) === "")) {
      assert.equal(cell.querySelector(".bar"), null, "an empty window draws a bar");
    }
  }));

test("UsageBand: drawing values at warn from 60 and crit from 80", (t) =>
  withApp(t, usage, async ({ container }) => {
    const levels = rowsOf(container).map((row) =>
      cellsOf(row)
        .slice(1, 4)
        .map((cell) => cell.getAttribute("class") ?? ""),
    );
    assert.deepEqual(levels, [
      ["uv d5 crit", "uv d7", ""],
      ["uv d5 warn", "uv d7 warn", "uv fb"],
      ["", "", ""],
      ["", "uv d7", "uv fb crit"],
    ]);
  }));

test("UsageBand: drawing the dot on each account a context names", (t) =>
  withApp(t, usage, async ({ container }) => {
    const filled = rowsOf(container)
      .filter((row) => row.classList.contains("inuse"))
      .map(nameOf);
    assert.deepEqual(filled, ["ember-1", "tide-3"]);
  }));

test("UsageBand: drawing the time to the 7d reset in the last column", (t) =>
  withApp(t, usage, async ({ container }) => {
    const resets = rowsOf(container).map((row) => cellsOf(row)[4]?.textContent ?? "");
    assert.deepEqual(resets, ["2d", "3d", "", "12h"]);
  }));

test("UsageBand: the gear opens #/accounts", (t) =>
  withApp(t, usage, async ({ container, reports, settle }) => {
    const gear = band(container).querySelector<HTMLButtonElement>('button[aria-label="Accounts and routes"]');
    assert.ok(gear !== null, "the band has no gear");
    await press(gear);
    await settle(() => reports.some((report) => report.form === "#/accounts"), "the accounts address");
    assert.ok(
      reports.some((report) => report.form === "#/accounts" && report.mode === "push"),
      "the gear did not push #/accounts",
    );
    assert.ok(container.querySelector(".accounts-read") !== null, "the accounts page is not drawn");
  }));

test("UsageBand: not drawn until usage has answered with an account", (t) =>
  withApp(t, noUsage, async ({ container, draw }) => {
    assert.equal(container.querySelector(".uband"), null, "the band is drawn before usage has answered");
    await draw({ ...noUsage, answered: true });
    assert.equal(container.querySelector(".uband"), null, "the band is drawn with no account");
    await draw(usage);
    assert.ok(container.querySelector(".uband") !== null, "the band is not drawn once usage has an account");
  }));
