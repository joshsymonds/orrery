import type { ComponentChildren, JSX } from "preact";

import { Icon, type IconName } from "./glyphs";
import { Markdown } from "./markdown";
import { clock } from "./reader";

// Tone is the tone a block draws in, its rail's and its label's: the owner's words, the engine's, a verdict that passed or
// failed, something that asks, or a quiet end.
type Tone = "you" | "engine" | "ok" | "bad" | "attention" | "quiet";

// BlockKind is the kind a block's root carries as a class, so the stylesheet tells the blocks apart.
type BlockKind = "you" | "event" | "stepend" | "epicevent" | "checks";

// BlockProps is what a Block draws: its tone, kind, icon and label; then who spoke, the words after the label, the time,
// and the end, each when given; then the children as the body. A pending block is one still being sent.
interface BlockProps {
  tone: Tone;
  kind: BlockKind;
  icon: IconName;
  label: string;
  who?: string;
  words?: string;
  time?: string;
  end?: ComponentChildren;
  pending?: boolean;
  children?: ComponentChildren;
}

// Block draws someone speaking. The root is the one block element, and its tone draws the rail on its left. Its head holds
// the icon and the label line, and its children follow as the body.
export function Block({
  tone,
  kind,
  icon,
  label,
  who,
  words,
  time,
  end,
  pending = false,
  children,
}: BlockProps): JSX.Element {
  return (
    <div class={`block ${tone} ${kind}${pending ? " pending" : ""}`} data-shape="block" data-tone={tone}>
      <div class="head">
        <Icon name={icon} />
        <span class="label">{label}</span>
        {who ? <b class="who">{who}</b> : null}
        {words ? <span class="words">{words}</span> : null}
        {time ? <span class="at">{time}</span> : null}
        {end !== undefined ? <span class="end">{end}</span> : null}
      </div>
      {children}
    </div>
  );
}

// YouBlock draws your words: the person icon, YOU and the time, then the text as Markdown with the find query's marks. A
// message the page sent and has not stored yet is SENDING: pending, and drawn with no time.
export function YouBlock({
  text,
  time,
  sending = false,
  query = "",
  current = -1,
}: {
  text: string;
  time?: bigint;
  sending?: boolean;
  query?: string;
  current?: number;
}): JSX.Element {
  return (
    <Block
      tone="you"
      kind="you"
      icon="person"
      label={sending ? "SENDING" : "YOU"}
      time={sending || time === undefined ? undefined : clock(time)}
      pending={sending}
    >
      <Markdown text={text} query={query} current={current} />
    </Block>
  );
}

// EventBlock draws what the engine sent: the gear icon and EVENT from the engine, or for a cron prompt the clock icon and
// CRON, and its time. Its text is Markdown.
export function EventBlock({ text, time, cron = false }: { text: string; time?: bigint; cron?: boolean }): JSX.Element {
  return (
    <Block
      tone="engine"
      kind="event"
      icon={cron ? "clock" : "gear"}
      label={cron ? "CRON" : "EVENT"}
      words={cron ? undefined : "from the engine"}
      time={time === undefined ? undefined : clock(time)}
    >
      <Markdown text={text} />
    </Block>
  );
}
