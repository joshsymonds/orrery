import type { JSX } from "preact";

import type { ConversationContext, HostStatusResponse } from "./gen/orrery/v1/orrery_pb";
import { context } from "./tokens/rules";

// StripHost is the host a strip names: its name and the stats it draws. other marks a host this server cannot read: the
// strip names it and draws no stats.
export type StripHost = Pick<HostStatusResponse, "host" | "stats"> & { other?: boolean };

// HostStat is one of a host's stats as Host.Status answers it.
type HostStat = HostStatusResponse["stats"][number];

// seriesCount is how many series colours a host's stats take in turn: the stat at index i takes series
// (i mod seriesCount) + 1.
const seriesCount = 5;

// contextBlocks are the blocks a context draws, numbered from one, each filled from its own percentPerBlock of the
// window.
const contextBlocks = Array.from({ length: context.blocks }, (_, index) => index + 1);
const percentPerBlock = context.percentPerBlock;

// ContextFigure is a context as the strip draws it: its percent of the window, the blocks it fills, and the band its
// percent falls in.
export interface ContextFigure {
  percent: number;
  blocks: number;
  band: "ok" | "warn" | "crit";
}

// PaneStrip is a page's strip, drawn under its title row: the host's name with each of its stats, and a pane
// conversation's context. With no host it draws no host part, and a context with no tokens or no window draws no context
// part. A strip with neither draws nothing.
export function PaneStrip({
  host,
  context,
}: {
  host: StripHost | null;
  context?: ConversationContext;
}): JSX.Element | null {
  const figure = context === undefined ? null : contextFigure(context);
  if (host === null && figure === null) {
    return null;
  }
  const stats: readonly HostStat[] = host === null || host.other === true ? [] : host.stats;
  return (
    <div class="pane-strip">
      {host !== null && <span class="pane-strip-host">{host.host}</span>}
      {host?.other === true && <span class="other">on another host: no meters</span>}
      {stats.map((stat, index) => (
        <Meter key={index} stat={stat} series={(index % seriesCount) + 1} />
      ))}
      {context !== undefined && <ContextMeter context={context} />}
    </div>
  );
}

// Meter draws one host stat: its name, a bar at its percent when it has one, and its value, in the series the stat takes
// from its place among the host's stats and at its level.
function Meter({ stat, series }: { stat: HostStat; series: number }): JSX.Element {
  return (
    <span class={`meter series-${series} level-${stat.level}`}>
      <span class="name">{stat.label}</span>
      {stat.percent !== undefined && (
        <span class="bar">
          <i style={{ width: `${stat.percent}%` }} />
        </span>
      )}
      <span class="value">{stat.value}</span>
    </span>
  );
}

// ContextMeter draws a conversation's context: five blocks, the filled ones on, and its percent, at its band. Unless bare,
// it also names the context and says its tokens of the window. A context with no tokens or no window draws nothing. A
// caller that has already read the context's figure passes it, so it is read once.
export function ContextMeter({
  context,
  bare = false,
  figure = contextFigure(context),
}: {
  context: ConversationContext;
  bare?: boolean;
  figure?: ContextFigure | null;
}): JSX.Element | null {
  if (figure === null) {
    return null;
  }
  return (
    <span class="ctx" data-level={figure.band}>
      {!bare && <span class="name">context</span>}
      <span class="blocks">
        {contextBlocks.map((block) => (
          <i key={block} class={block <= figure.blocks ? "on" : undefined} />
        ))}
      </span>
      <span class="pct">{`${Math.round(figure.percent)}%`}</span>
      {!bare && <span class="used">{`${tokensText(context.tokens)} of ${tokensText(context.window)}`}</span>}
    </span>
  );
}

// contextFigure reads a context as the strip draws it: the percent of its window, clamped to 0..100, the blocks that
// percent fills, and its band. A context with no tokens or no window has no figure.
export function contextFigure(context: ConversationContext): ContextFigure | null {
  if (context.tokens === 0n || context.window === 0n) {
    return null;
  }
  const percent = Math.min(100, Math.max(0, (Number(context.tokens) * 100) / Number(context.window)));
  return {
    percent,
    blocks: Math.max(percent > 0 ? 1 : 0, Math.round(percent / percentPerBlock)),
    band: bandOf(percent),
  };
}

// bandOf is the level a percent falls in, read from rules' context limits: ok below the first limit, warn below the
// second, and crit from the second on.
function bandOf(percent: number): ContextFigure["band"] {
  if (percent < context.limits[0]) {
    return "ok";
  }
  if (percent < context.limits[1]) {
    return "warn";
  }
  return "crit";
}

// tokensText writes a token count as the strip shows it: thousands under a million, as 152k, and millions from there, as
// 1M, in whole numbers.
function tokensText(tokens: bigint): string {
  const thousands = Math.round(Number(tokens) / 1000);
  return thousands < 1000 ? `${thousands}k` : `${Math.round(Number(tokens) / 1000000)}M`;
}
