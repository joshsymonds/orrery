// The figures every usage view shares: a session's totals summed, its four token kinds, its model-and-agent rows, and the
// formats its dollars and tokens are written in. Each is a pure function of the totals, which are the rows SessionTotals
// answers for a session.

import type { UsageSessionTotal } from "./gen/orrery/v1/usage_pb";

// dollars writes a cost in nano US dollars as dollars to two decimals: 3820000000 is $3.82.
export function dollars(nano: bigint): string {
  return `$${(Number(nano) / 1e9).toFixed(2)}`;
}

// tokens writes a count of tokens short: thousands under a million, one decimal of millions under ten million, and whole
// millions from ten million: 999000 is 999k, 1500000 is 1.5M, and 48000000 is 48M.
export function tokens(n: bigint): string {
  const count = Number(n);
  if (count < 1_000_000) {
    return `${Math.round(count / 1000)}k`;
  }
  if (count < 10_000_000) {
    return `${(count / 1e6).toFixed(1)}M`;
  }
  return `${Math.round(count / 1e6)}M`;
}

// SessionSum is the totals of a session: its requests, its billed and list costs in nano US dollars, and its four token
// kinds.
export interface SessionSum {
  requests: bigint;
  billed: bigint;
  list: bigint;
  inputTokens: bigint;
  outputTokens: bigint;
  cacheReadTokens: bigint;
  cacheWriteTokens: bigint;
}

// sessionSum adds up the totals of one session.
export function sessionSum(totals: readonly UsageSessionTotal[]): SessionSum {
  return totals.reduce<SessionSum>(
    (sum, total) => ({
      requests: sum.requests + total.requests,
      billed: sum.billed + total.billedCostNanoUsd,
      list: sum.list + total.listCostNanoUsd,
      inputTokens: sum.inputTokens + total.inputTokens,
      outputTokens: sum.outputTokens + total.outputTokens,
      cacheReadTokens: sum.cacheReadTokens + total.cacheReadTokens,
      cacheWriteTokens: sum.cacheWriteTokens + total.cacheWriteTokens,
    }),
    { requests: 0n, billed: 0n, list: 0n, inputTokens: 0n, outputTokens: 0n, cacheReadTokens: 0n, cacheWriteTokens: 0n },
  );
}

// KindName is one of the four token kinds a request uses.
export type KindName = "input" | "output" | "cacheRead" | "cacheWrite";

// KindFigure is one token kind of a session: its tokens, and its list cost in nano US dollars when those are known.
export interface KindFigure {
  kind: KindName;
  label: string;
  tokens: bigint;
  dollars: bigint | undefined;
}

// KindsView is a session's four token kinds, in the order input, output, cache read and cache write. hasDollars is true
// only when every total sets all four kind costs and they sum to the session's list cost; then each kind carries its
// dollars, and otherwise every kind's dollars are undefined.
export interface KindsView {
  hasDollars: boolean;
  kinds: KindFigure[];
}

// kindFields names each token kind with its label, its tokens and its list cost, which is unset when a total does not set
// it.
const kindFields: readonly {
  kind: KindName;
  label: string;
  tokens: (total: UsageSessionTotal) => bigint;
  list: (total: UsageSessionTotal) => bigint | undefined;
}[] = [
  {
    kind: "input",
    label: "input",
    tokens: (total) => total.inputTokens,
    list: (total) => total.listInputNanoUsd,
  },
  {
    kind: "output",
    label: "output",
    tokens: (total) => total.outputTokens,
    list: (total) => total.listOutputNanoUsd,
  },
  {
    kind: "cacheRead",
    label: "cache read",
    tokens: (total) => total.cacheReadTokens,
    list: (total) => total.listCacheReadNanoUsd,
  },
  {
    kind: "cacheWrite",
    label: "cache write",
    tokens: (total) => total.cacheWriteTokens,
    list: (total) => total.listCacheWriteNanoUsd,
  },
];

// kindsView sums each of a session's four token kinds, and gives each its dollars when the kind costs are all known.
export function kindsView(totals: readonly UsageSessionTotal[]): KindsView {
  const allKnown = totals.every((total) => kindFields.every((field) => field.list(total) !== undefined));
  const kindCostSum = totals.reduce(
    (sum, total) => kindFields.reduce((kindSum, field) => kindSum + (field.list(total) ?? 0n), sum),
    0n,
  );
  const listSum = totals.reduce((sum, total) => sum + total.listCostNanoUsd, 0n);
  const hasDollars = allKnown && kindCostSum === listSum;
  const kinds = kindFields.map((field) => ({
    kind: field.kind,
    label: field.label,
    tokens: totals.reduce((sum, total) => sum + field.tokens(total), 0n),
    dollars: hasDollars ? totals.reduce((sum, total) => sum + (field.list(total) ?? 0n), 0n) : undefined,
  }));
  return { hasDollars, kinds };
}

// ModelRow is one total of a session as a row: its model, its agent type, which reads main when the total has none, its
// billed and list costs in nano US dollars, and its share of the session's list cost, from 0 to 1.
export interface ModelRow {
  model: string;
  agent: string;
  billed: bigint;
  list: bigint;
  share: number;
}

// compareText orders two strings by their code points, so the order does not depend on the locale.
function compareText(a: string, b: string): number {
  if (a < b) {
    return -1;
  }
  return a > b ? 1 : 0;
}

// modelRows gives one row per total, largest list cost first, ties by model and then by agent.
export function modelRows(totals: readonly UsageSessionTotal[]): ModelRow[] {
  const listSum = sessionSum(totals).list;
  return totals
    .map((total) => ({
      model: total.model,
      agent: total.agentType === "" ? "main" : total.agentType,
      billed: total.billedCostNanoUsd,
      list: total.listCostNanoUsd,
      share: listSum === 0n ? 0 : Number(total.listCostNanoUsd) / Number(listSum),
    }))
    .sort((a, b) => {
      if (a.list !== b.list) {
        return a.list > b.list ? -1 : 1;
      }
      return compareText(a.model, b.model) || compareText(a.agent, b.agent);
    });
}
