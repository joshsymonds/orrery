// The reads that keep the usage a page holds current: each tick while the page is shown reads the accounts, the route
// registry and the limit readings, and the seat spend once a minute. A tick while the page is hidden makes no call, and a
// tick while a read is still in flight makes no new one.

import { useEffect, useState } from "preact/hooks";

import type { createClients } from "./client";
import { spanSeconds, type UsageData, noUsage } from "./usagedata";

// spendEverySeconds is how long after a Spend read is made before the next one is made.
const spendEverySeconds = 60;

// Clients is the part of createClients the reads use.
type Clients = Pick<ReturnType<typeof createClients>, "accounts" | "routes" | "usage">;

// Outcome is what one call of a tick gives: its answer, or that it failed.
type Outcome<T> = { readonly ok: true; readonly value: T } | { readonly ok: false };

// settle runs call and gives its answer, or that it failed. It never throws.
async function settle<T>(call: () => Promise<T>): Promise<Outcome<T>> {
  try {
    return { ok: true, value: await call() };
  } catch {
    return { ok: false };
  }
}

export interface UsageReadsOptions {
  // clients holds the accounts, routes and usage clients, as createClients makes them.
  clients: Clients;
  // hidden says whether the page is hidden. A tick while it is makes no call.
  hidden: () => boolean;
  // now is the time in unix seconds.
  now: () => number;
  // onChange is called with the data after each read lands, whether the read succeeded or failed.
  onChange: (data: UsageData) => void;
}

export interface UsageReads {
  // tick starts a read, unless the page is hidden or a read is still in flight.
  tick(): void;
  // current is the data held from the reads that have landed.
  current(): UsageData;
}

// createUsageReads returns the reads a page ticks to keep its usage current.
export function createUsageReads({ clients, hidden, now, onChange }: UsageReadsOptions): UsageReads {
  let data: UsageData = noUsage;
  let inFlight = false;
  // lastSpendAt is the time the latest Spend read was made, or null when none has been.
  let lastSpendAt: number | null = null;
  // answered records which of the reads that make up answered has answered at least once.
  const answered = { accounts: false, routes: false, readings: false };

  // read makes one tick's calls and holds what they answer. A call that fails keeps what was held for it, and marks the
  // read failed. Spend is read only when no Spend read was made in the last spendEverySeconds.
  async function read(): Promise<void> {
    try {
      const at = now();
      const spendDue = lastSpendAt === null || at - lastSpendAt >= spendEverySeconds;
      if (spendDue) {
        lastSpendAt = at;
      }
      const to = Math.floor(at);
      const [accounts, routes, readings, spend] = await Promise.all([
        settle(() => clients.accounts.list({})),
        settle(() => clients.routes.get({})),
        settle(() => clients.usage.readings({})),
        spendDue ? settle(() => clients.usage.spend({ from: BigInt(to - spanSeconds), to: BigInt(to) })) : null,
      ]);
      const next: UsageData = { ...data };
      if (accounts.ok) {
        next.accounts = accounts.value.accounts;
        answered.accounts = true;
      }
      if (routes.ok) {
        next.registry = routes.value.registry;
        answered.routes = true;
      }
      if (readings.ok) {
        next.readings = readings.value.readings;
        answered.readings = true;
      }
      if (spend?.ok) {
        next.spend = spend.value.seats;
      }
      next.answered = answered.accounts && answered.routes && answered.readings;
      next.failed = [accounts, routes, readings, spend].some((outcome) => outcome !== null && !outcome.ok);
      data = next;
      onChange(data);
    } finally {
      inFlight = false;
    }
  }

  return {
    tick() {
      if (hidden() || inFlight) {
        return;
      }
      inFlight = true;
      void read();
    },
    current: () => data,
  };
}

// useUsage holds the usage that Accounts.List, Routes.Get and Usage.Readings answer. It reads them together while enabled
// is true, and holds nothing new while enabled is false. A read that fails keeps what was held and sets failed. The
// spend is not read here, so the spend the usage holds stays null. It reads again each time it becomes enabled, and it
// runs no timer.
export function useUsage(clients: Clients, enabled: boolean): UsageData {
  const [data, setData] = useState<UsageData>(noUsage);
  useEffect(() => {
    if (!enabled) {
      return;
    }
    let live = true;
    void Promise.all([clients.accounts.list({}), clients.routes.get({}), clients.usage.readings({})]).then(
      ([listed, got, readings]) => {
        if (live) {
          setData((previous) => ({
            ...previous,
            accounts: listed.accounts,
            registry: got.registry,
            readings: readings.readings,
            answered: true,
            failed: false,
          }));
        }
      },
      () => {
        if (live) {
          setData((previous) => ({ ...previous, failed: true }));
        }
      },
    );
    return () => {
      live = false;
    };
  }, [clients, enabled]);
  return data;
}
