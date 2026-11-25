// UsagePanel draws a session's usage as the Usage panel does: its list total at list price with its requests and what it
// was billed, its four token kinds as one bar with a legend, and its model-and-agent rows, largest first, each with billed
// beside list and a bar of its share. The bar weighs its kinds by dollars when every kind's cost is known, and by tokens
// otherwise. headless leaves out the head, for a panel in a sheet whose title already names it, as ListPanel does.

import type { UsageSessionTotal } from "./gen/orrery/v1/usage_pb";
import { dollars, type KindFigure, type KindName, kindsView, modelRows, sessionSum, tokens } from "./usagefigures";

// kindClass gives each kind the colour class its segment and its legend mark take.
const kindClass: Record<KindName, string> = { input: "kb1", output: "kb4", cacheRead: "kb3", cacheWrite: "kb2" };

// kindOrder is the order the bar and its legend draw the kinds in, as round 8 section E draws them: input, cache write,
// cache read, output.
const kindOrder: readonly KindName[] = ["input", "cacheWrite", "cacheRead", "output"];

// weight is the figure a kind's segment is drawn by: its dollars when the view has them, and its tokens otherwise.
function weight(kind: KindFigure, hasDollars: boolean): number {
  return hasDollars && kind.dollars !== undefined ? Number(kind.dollars) : Number(kind.tokens);
}

// share writes a part of a whole as a CSS width: 0 when the whole is 0, so an empty session draws no segment.
function share(part: number, whole: number): string {
  return `${whole === 0 ? 0 : (part / whole) * 100}%`;
}

export function UsagePanel({ totals, headless = false }: { totals: readonly UsageSessionTotal[]; headless?: boolean }) {
  const sum = sessionSum(totals);
  const view = kindsView(totals);
  const kinds = [...view.kinds].sort((a, b) => kindOrder.indexOf(a.kind) - kindOrder.indexOf(b.kind));
  const rows = modelRows(totals);
  const weights = kinds.map((kind) => weight(kind, view.hasDollars));
  const whole = weights.reduce((total, figure) => total + figure, 0);
  const subscription = sum.billed === 0n && sum.list > 0n;
  return (
    <div class="usage">
      {headless ? null : <h2>Usage</h2>}
      <div class="usage-total">
        <span class="usage-list">{dollars(sum.list)}</span>
        <span class="usage-sub">
          <span class="usage-requests">
            at list price · {Number(sum.requests).toLocaleString("en-US")} requests
          </span>
          <span class="usage-billed">
            billed {dollars(sum.billed)}
            {subscription ? " · subscription" : ""}
          </span>
        </span>
      </div>
      <div class="usage-kinds">
        <div class="kindbar">
          {kinds.map((kind, i) => (
            <i class={kindClass[kind.kind]} style={{ width: share(weights[i], whole) }} />
          ))}
        </div>
        <div class={view.hasDollars ? "usage-legend" : "usage-legend tokens-only"}>
          {kinds.map((kind) => (
            <div class="usage-kind">
              <i class={kindClass[kind.kind]} />
              <span class="usage-kind-name">{kind.label}</span>
              <span class="usage-kind-tokens">{tokens(kind.tokens)}</span>
              {kind.dollars === undefined ? null : (
                <span class="usage-kind-dollars">{dollars(kind.dollars)}</span>
              )}
            </div>
          ))}
        </div>
      </div>
      <span class="usage-label">by model and agent</span>
      <div class="usage-rows">
        {rows.map((row) => (
          <div class="usage-row">
            <span class="usage-who">
              {row.model} <span class="usage-agent">{row.agent}</span>
            </span>
            <span class="usage-bar">
              <i style={{ width: `${row.share * 100}%` }} />
            </span>
            <span class="usage-row-billed">billed {dollars(row.billed)}</span>
            <span class="usage-row-list">{dollars(row.list)}</span>
          </div>
        ))}
      </div>
    </div>
  );
}
