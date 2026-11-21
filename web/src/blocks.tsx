import type { ComponentChildren, JSX } from "preact";

import { useState } from "preact/hooks";

import type { EpicStepCheck } from "./gen/orrery/v1/epics_pb";
import { Icon, type IconName } from "./glyphs";
import { Markdown } from "./markdown";
import { clock } from "./reader";

// Tone is the tone a block draws in, its rail's and its label's: the owner's words, the engine's, a verdict that passed or
// failed, something that asks, or a quiet end.
type Tone = "you" | "engine" | "ok" | "bad" | "attention" | "quiet";

// BlockKind is the kind a block's root carries as a class, so the stylesheet tells the blocks apart.
type BlockKind = "you" | "event" | "stepend" | "epicevent" | "checks";

// BlockProps is what a Block draws: its tone, kind, icon and label; then who spoke, the words after the label, the time,
// and the end, each when given; then the children as the body. A gauge, when given, leads the head before the icon. A
// pending block is one still being sent.
interface BlockProps {
  tone: Tone;
  kind: BlockKind;
  icon: IconName;
  label: string;
  gauge?: ComponentChildren;
  who?: string;
  words?: string;
  time?: string;
  end?: ComponentChildren;
  pending?: boolean;
  children?: ComponentChildren;
}

// Block draws someone speaking. The root is the one block element, and its tone draws the rail on its left. Its head holds
// the gauge when it has one, then the icon and the label line, and its children follow as the body.
export function Block({
  tone,
  kind,
  icon,
  label,
  gauge,
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
        {gauge}
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

// CheckBlock draws a send-back round: CHECKS, the round it is and from the engine, and its time. Each check of that round
// is a chip, with the check icon when it passed and the cross when it failed. Its tone is bad when one failed and ok when
// they all passed; with no check of the round it draws no chips and the engine's tone. The text's first line is the note,
// and with more lines a chevron in the end opens the rest under it. open is whether the rest starts open.
export function CheckBlock({
  text,
  round,
  checks,
  time,
  open = false,
}: {
  text: string;
  round: number;
  checks: EpicStepCheck[];
  time?: bigint;
  open?: boolean;
}): JSX.Element {
  const [shown, setShown] = useState(open);
  const roundChecks = checks.filter((check) => check.round === round);
  const tone: Tone = roundChecks.length === 0 ? "engine" : roundChecks.some((check) => !check.ok) ? "bad" : "ok";
  const [note, ...rest] = text.split("\n");
  const opens = rest.length > 0;
  const chips = roundChecks.map((check, index) => (
    <span key={index} class={check.ok ? "chip ok" : "chip bad"}>
      <Icon name={check.ok ? "check" : "cross"} />
      {check.name}
    </span>
  ));
  const end =
    chips.length > 0 || opens ? (
      <>
        {chips}
        {opens && (
          <button
            type="button"
            aria-label={shown ? "Hide the rest" : "Show the rest"}
            aria-expanded={shown}
            onClick={() => setShown((value) => !value)}
          >
            <Icon name={shown ? "chevronDown" : "chevronRight"} />
          </button>
        )}
      </>
    ) : undefined;
  return (
    <Block
      tone={tone}
      kind="checks"
      icon={tone === "ok" ? "check" : tone === "bad" ? "cross" : "gear"}
      label="CHECKS"
      words={`round ${round} from the engine`}
      time={time === undefined ? undefined : clock(time)}
      end={end}
    >
      <Markdown text={note} />
      {opens && shown && (
        <div class="opened">
          <Markdown text={rest.join("\n")} />
        </div>
      )}
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
