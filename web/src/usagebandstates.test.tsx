import assert from "node:assert/strict";
import { type TestContext, test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { Code, createRouterTransport } from "@connectrpc/connect";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients } from "./client";
import { accountsServe } from "./fixtures/accounts";
import { type UsageServe, usageServe } from "./fixtures/usage";
import { type Account, AccountSchema, RouteRegistrySchema } from "./gen/orrery/v1/accounts_pb";
import { type UsageReading, UsageReadingSchema } from "./gen/orrery/v1/usage_pb";
import type { SeenClock } from "./seen";
import { mockTimers } from "./testclock";
import { assertNone, installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until } from "./testwait";
import { createUsageReads, type UsageReads } from "./usageload";
import { noUsage, type UsageData } from "./usagedata";

// wideWidth is the window width the band is drawn at, past the 760px query the inbox's stacked layout follows.
const wideWidth = 1024;
// now is the fake clock's time in unix seconds. Every reset and observed time below is set from it.
const now = 1_800_000_000;
const hour = 3600;
const day = 86400;

// Seed is the invented accounts and readings the fakes answer with, and the usage a draw of the band is given.
interface Seed {
  accounts: string[];
  readings: UsageReading[];
}

// account is one invented account of the id given.
function account(id: string): Account {
  return create(AccountSchema, {
    id,
    name: id,
    provider: "anthropic",
    plan: "Claude Max",
    fingerprint: `fp-${id}`,
    created: 1n,
  });
}

// reading is one invented limit reading. Its status is allowed, its share of the limit 30 percent, its reset two hours
// after now and its observation a minute before now, unless the spec gives otherwise.
interface ReadingSpec {
  account: string;
  claim: string;
  status?: string;
  utilization?: number;
  reset?: number;
  observed?: number;
  dropped?: boolean;
}

function reading(spec: ReadingSpec): UsageReading {
  return create(UsageReadingSchema, {
    account: spec.account,
    claim: spec.claim,
    status: spec.status ?? "allowed",
    utilization: spec.utilization ?? 0.3,
    reset: BigInt(spec.reset ?? now + 2 * hour),
    observed: BigInt(spec.observed ?? now - 60),
    dropped: spec.dropped ?? false,
  });
}

// usageOf is the usage the page holds once the reads have answered: the seed's accounts and readings.
function usageOf(seed: Seed): UsageData {
  return { ...noUsage, accounts: seed.accounts.map(account), readings: seed.readings, answered: true };
}

// stillClock is a seen clock whose timers never fire, so no row is marked seen.
const stillClock: SeenClock = {
  now: () => now * 1000,
  setTimeout: () => 0,
  clearTimeout: () => {},
};

// Mounted is the App drawn over the fakes, with what a test checks: the container it draws into, the fakes the reads call,
// the clients they go through, and draw, which draws the App with the usage given.
interface Mounted {
  container: HTMLElement;
  usage: UsageServe;
  clients: ReturnType<typeof createClients>;
  draw(next: UsageData): Promise<void>;
}

// withApp mounts the App over the fakes seeded with seed, in a fresh DOM at the wide width, with its timers on the fake
// clock, and runs check. The App is unmounted and the DOM removed when check returns or throws.
async function withApp(t: TestContext, seed: Seed, check: (mounted: Mounted) => Promise<void>): Promise<void> {
  mockTimers(t, ["setTimeout", "setInterval"]);
  const dom = installTestDom();
  dom.innerWidth = wideWidth;
  const accounts = accountsServe({ accounts: seed.accounts.map(account), registry: create(RouteRegistrySchema, {}) });
  const usage = usageServe({ readings: seed.readings, spend: [] });
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
    await removeTestDom(dom);
  });
  const draw = async (next: UsageData) => {
    await act(() => {
      render(
        <App
          rows={[]}
          now={now}
          clients={clients}
          seenClock={stillClock}
          usage={next}
          onAddress={() => undefined}
        />,
        container,
      );
    });
  };
  try {
    await check({ container, usage, clients, draw });
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

// rowNamed is the band's row for the account named.
function rowNamed(container: ParentNode, name: string): HTMLElement {
  const row = rowsOf(container).find((candidate) => nameOf(candidate) === name);
  assert.ok(row !== undefined, `the band has no row for ${name}`);
  return row;
}

// cellsOf is a row's cells in column order: the name, the 5h, 7d and Fable windows, then the last column.
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

// windowsOf is a row's 5h, 7d and Fable values, "" for a window with no number.
function windowsOf(row: HTMLElement): string[] {
  return cellsOf(row).slice(1, 4).map(valueOf);
}

// lastOf is what a row's last column reads.
function lastOf(row: HTMLElement): string {
  return row.querySelector(".urs")?.textContent ?? "";
}

// noteOf is the note a row reads, or "" when it has none.
function noteOf(row: HTMLElement): string {
  return row.querySelector(".note")?.textContent ?? "";
}

// landed waits until n reads have landed, that is until onChange has been called n times.
function landed(t: TestContext, changes: readonly UsageData[], n: number): Promise<void> {
  return until(t.signal, () => changes.length === n, `read ${n} to land`, { step: () => turn() });
}

test("UsageBandStates: a dropped reading, and one whose reset has passed, are not drawn and limit nothing", (t) => {
  const seed: Seed = {
    accounts: ["gull-5", "wren-6", "heron-7"],
    readings: [
      reading({ account: "gull-5", claim: "5h", status: "rejected", utilization: 1, reset: now + hour, dropped: true }),
      reading({ account: "wren-6", claim: "5h", status: "rejected", utilization: 1, reset: now - 60 }),
      reading({ account: "wren-6", claim: "7d", utilization: 0.9, reset: now - 60 }),
      reading({ account: "heron-7", claim: "5h", utilization: 0.9, reset: now - 60 }),
      reading({ account: "heron-7", claim: "7d", utilization: 0.5, reset: now + 2 * day }),
    ],
  };
  return withApp(t, seed, async ({ container, draw }) => {
    await draw(usageOf(seed));
    for (const name of ["gull-5", "wren-6"]) {
      const row = rowNamed(container, name);
      assertNone(row.querySelector(".limg"), `${name} is drawn as limited`);
      assert.ok(row.querySelector(".udot") !== null, `${name} has no dot`);
      assert.deepEqual(windowsOf(row), ["", "", ""], `${name} draws a reading that is not current`);
      assert.equal(noteOf(row), "no reading yet", `${name} does not read no reading yet`);
    }
    const heron = rowNamed(container, "heron-7");
    assertNone(heron.querySelector(".limg"), "heron-7 is drawn as limited by a passed reset");
    assert.deepEqual(windowsOf(heron), ["", "50%", ""], "heron-7 draws a 5h reading whose reset has passed");
  });
});

test("UsageBandStates: a rejected 5h, 7d or 7d_oi puts the limited glyph in the dot's place, with the time to the latest reset", (t) => {
  const seed: Seed = {
    accounts: ["lark-8", "lark-9", "lark-10", "lark-11"],
    readings: [
      reading({ account: "lark-8", claim: "5h", status: "rejected", utilization: 1, reset: now + hour }),
      reading({ account: "lark-9", claim: "7d", status: "rejected", utilization: 0.96, reset: now + 2 * day }),
      reading({ account: "lark-10", claim: "7d_oi", status: "rejected", utilization: 0.8, reset: now + 3 * day }),
      reading({ account: "lark-11", claim: "5h", status: "rejected", utilization: 1, reset: now + hour }),
      reading({ account: "lark-11", claim: "7d", status: "rejected", utilization: 0.96, reset: now + 2 * day }),
    ],
  };
  return withApp(t, seed, async ({ container, draw }) => {
    await draw(usageOf(seed));
    const expected: [string, string][] = [
      ["lark-8", "1h"],
      ["lark-9", "2d"],
      ["lark-10", "3d"],
      ["lark-11", "2d"],
    ];
    for (const [name, lifts] of expected) {
      const row = rowNamed(container, name);
      assert.ok(row.querySelector(".uname .limg") !== null, `${name} has no limited glyph in its dot's place`);
      assertNone(row.querySelector(".udot"), `${name} still draws its dot beside the limited glyph`);
      assert.equal(lastOf(row), lifts, `${name} does not show the time to its latest reset`);
    }
  });
});

test("UsageBandStates: an overage-only rejection draws the account as not limited", (t) => {
  const seed: Seed = {
    accounts: ["mink-12"],
    readings: [
      reading({ account: "mink-12", claim: "overage", status: "rejected", utilization: 1, reset: now + hour }),
      reading({ account: "mink-12", claim: "5h", utilization: 0.2, reset: now + 2 * hour }),
    ],
  };
  return withApp(t, seed, async ({ container, draw }) => {
    await draw(usageOf(seed));
    const row = rowNamed(container, "mink-12");
    assertNone(row.querySelector(".limg"), "an overage rejection drew the limited glyph");
    assert.ok(row.querySelector(".udot") !== null, "an overage rejection drew no dot");
    assert.deepEqual(windowsOf(row), ["20%", "", ""], "the account's fresh 5h is not drawn");
    assert.equal(lastOf(row), "", "an overage rejection drew a time in the last column");
  });
});

test("UsageBandStates: a fresh 5h beside a 7d_oi observed over an hour before now draws the age, and an hour exactly does not", (t) => {
  const seed: Seed = {
    accounts: ["finch-13", "finch-14"],
    readings: [
      reading({ account: "finch-13", claim: "5h", utilization: 0.4, reset: now + 2 * hour, observed: now - 60 }),
      reading({
        account: "finch-13",
        claim: "7d_oi",
        utilization: 0.2,
        reset: now + 3 * day,
        observed: now - 2 * hour,
      }),
      reading({ account: "finch-14", claim: "5h", utilization: 0.4, reset: now + 2 * hour, observed: now - hour }),
    ],
  };
  return withApp(t, seed, async ({ container, draw }) => {
    await draw(usageOf(seed));
    const stale = rowNamed(container, "finch-13");
    assert.deepEqual(windowsOf(stale), ["40%", "", "20%"], "the fresh 5h or the 7d_oi is not drawn");
    assert.equal(lastOf(stale), "2h ago", "the age of the old reading is not in the last column");
    assert.ok(stale.querySelector(".urs.stale") !== null, "the age is not drawn as stale");
    const hourOld = rowNamed(container, "finch-14");
    assert.equal(lastOf(hourOld), "", "a reading an hour old reads as stale");
    assertNone(hourOld.querySelector(".urs.stale"), "a reading an hour old is drawn as stale");
  });
});

test("UsageBandStates: an account with no drawn reading reads no reading yet and draws no window", (t) => {
  const seed: Seed = {
    accounts: ["plover-15", "kestrel-16"],
    readings: [reading({ account: "kestrel-16", claim: "5h", utilization: 0.5 })],
  };
  return withApp(t, seed, async ({ container, draw }) => {
    await draw(usageOf(seed));
    const none = rowNamed(container, "plover-15");
    assert.equal(noteOf(none), "no reading yet", "a row with no reading does not read no reading yet");
    assert.deepEqual(windowsOf(none), ["", "", ""], "a row with no reading draws a window");
    assert.equal(lastOf(none), "", "a row with no reading draws a last column");
    const read = rowNamed(container, "kestrel-16");
    assert.equal(noteOf(read), "", "a row with a reading reads no reading yet");
    assert.equal(band(container).querySelectorAll(".note").length, 1, "more than one row reads no reading yet");
  });
});

test("UsageBandStates: no accounts draws no band, even with readings held, and the inbox is still drawn", (t) => {
  const seed: Seed = {
    accounts: [],
    readings: [reading({ account: "ghost-19", claim: "5h", status: "rejected", utilization: 1, reset: now + hour })],
  };
  return withApp(t, seed, async ({ container, draw }) => {
    await draw(usageOf(seed));
    assert.ok(container.querySelector(".inbox-head") !== null, "the inbox head is not drawn");
    assertNone(container.querySelector(".uband"), "the band is drawn with no accounts");
  });
});

test("UsageBandStates: a failed Usage.Readings read leaves the inbox and its rows drawn with the last data read", (t) => {
  const seed: Seed = {
    accounts: ["moss-17", "ember-18"],
    readings: [
      reading({ account: "moss-17", claim: "5h", status: "rejected", utilization: 1, reset: now + hour }),
      reading({ account: "ember-18", claim: "7d", utilization: 0.5 }),
    ],
  };
  return withApp(t, seed, async ({ container, usage, clients, draw }) => {
    const changes: UsageData[] = [];
    const reads: UsageReads = createUsageReads({
      clients,
      hidden: () => false,
      now: () => now,
      onChange: (data) => changes.push(data),
    });
    reads.tick();
    await landed(t, changes, 1);
    await draw(reads.current());
    const before = rowsOf(container).map(nameOf);
    usage.refuseNext("Usage.Readings", Code.Unavailable, "readings: busy");
    reads.tick();
    await landed(t, changes, 2);
    await draw(reads.current());
    assert.equal(reads.current().failed, true, "the failed read did not mark the data failed");
    assert.ok(container.querySelector(".inbox-head") !== null, "the inbox is not drawn after a failed read");
    assert.deepEqual(rowsOf(container).map(nameOf), before, "the failed read changed the rows drawn");
    const moss = rowNamed(container, "moss-17");
    assert.ok(moss.querySelector(".limg") !== null, "the failed read lost the last limit read");
  });
});
