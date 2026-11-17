import type { JSX } from "preact";

import type { ConversationContext, HostStatusResponse } from "./gen/orrery/v1/orrery_pb";

// StripHost is the host a strip names: its name and the stats it draws. A pane row on another host names that host with
// no stats.
export type StripHost = Pick<HostStatusResponse, "host" | "stats">;

// contextBlocks are the five blocks a context draws, each filled from its own fifth of the window.
const contextBlocks = [1, 2, 3, 4, 5];
const percentPerBlock = 20;

// ContextFigure is a context as the strip draws it: its percent of the window, the blocks it fills, and the band its
// percent falls in.
interface ContextFigure {
  percent: number;
  blocks: number;
  band: "green" | "yellow" | "peach" | "red";
}

// PaneStrip is a page's strip, drawn under its title row: the host's name with each of its stats, as a label, a value
// and, for a stat with a percent, a thin bar at that width; and a pane conversation's context, as five blocks and a
// percent. With no host it draws no host part, and a context with no tokens or no window draws no context part. A strip
// with neither draws nothing.
export function PaneStrip({
  host,
  context,
}: {
  host: StripHost | null;
  context?: ConversationContext;
}): JSX.Element | null {
  const figure = contextFigure(context);
  if (host === null && figure === null) {
    return null;
  }
  return (
    <div class="pane-strip">
      {host !== null && <span class="pane-strip-host">{host.host}</span>}
      {host?.stats.map((stat, index) => (
        <span key={index} class="pane-strip-stat">
          <span class="pane-strip-label">{stat.label}</span>
          <span class="pane-strip-value">{stat.value}</span>
          {stat.percent !== undefined && (
            <span class={`bar level-${stat.level}`} style={{ width: `${stat.percent}%` }} />
          )}
        </span>
      ))}
      {figure !== null && <ContextFigureLine figure={figure} />}
    </div>
  );
}

// contextFigure reads a context as the strip draws it: the percent of its window, clamped to 0..100. A context with no
// tokens or no window, or none at all, has no figure.
function contextFigure(context: ConversationContext | undefined): ContextFigure | null {
  if (context === undefined || context.tokens === 0n || context.window === 0n) {
    return null;
  }
  const percent = Math.min(100, Math.max(0, (Number(context.tokens) * 100) / Number(context.window)));
  return { percent, blocks: Math.round(percent / percentPerBlock), band: bandOf(percent) };
}

// bandOf is the colour band a percent falls in: green under 40, yellow under 60, peach under 80 and red from 80.
function bandOf(percent: number): ContextFigure["band"] {
  if (percent < 40) {
    return "green";
  }
  if (percent < 60) {
    return "yellow";
  }
  if (percent < 80) {
    return "peach";
  }
  return "red";
}

// ContextFigureLine draws a context's five blocks, the filled ones on, and its percent as a whole number.
function ContextFigureLine({ figure }: { figure: ContextFigure }) {
  return (
    <span class={`pane-strip-context band-${figure.band}`}>
      <span class="ctx-blocks">
        {contextBlocks.map((block) => (
          <i key={block} class={block <= figure.blocks ? "on" : undefined} />
        ))}
      </span>
      <span class="ctx-pct">{`${Math.round(figure.percent)}%`}</span>
    </span>
  );
}
