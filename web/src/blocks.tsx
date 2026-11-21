import type { ComponentChildren, JSX } from "preact";

import { useState } from "preact/hooks";

import type { EpicStepCheck } from "./gen/orrery/v1/epics_pb";
import { Icon, type IconName } from "./glyphs";
import { partCurrent, useReveal } from "./lines";
import { Markdown, drawnText } from "./markdown";
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
// message the page sent and has not stored yet is SENDING: pending, and drawn with no time. A prompt sent from the phone
// draws the phone icon in place of the person icon, and the words from your phone after the label.
export function YouBlock({
  text,
  time,
  sending = false,
  phone = false,
  query = "",
  current = -1,
}: {
  text: string;
  time?: bigint;
  sending?: boolean;
  phone?: boolean;
  query?: string;
  current?: number;
}): JSX.Element {
  return (
    <Block
      tone="you"
      kind="you"
      icon={phone ? "phone" : "person"}
      label={sending ? "SENDING" : "YOU"}
      words={phone ? "· from your phone" : undefined}
      time={sending || time === undefined ? undefined : clock(time)}
      pending={sending}
    >
      <Markdown text={text} query={query} current={current} />
    </Block>
  );
}

// checkLines splits a check round's text into its note, its first line, and the rest under it, which opens under the note.
export function checkLines(text: string): { note: string; rest: string[] } {
  const [note, ...rest] = text.split("\n");
  return { note, rest };
}

// CheckBlock draws a send-back round: CHECKS, the round it is and from the engine, and its time. Each check of that round
// is a chip, with the check icon when it passed and the cross when it failed. Its tone is bad when one failed and ok when
// they all passed; with no check of the round it draws no chips and the engine's tone. The text's first line is the note,
// and with more lines a chevron in the end opens the rest under it. open is whether the rest starts open. The note and the
// rest are marked with the find's query, and the rest opens when the current match moves into it. query and current are
// as YouBlock's are, current being an offset in drawnText of the whole text, the note and the rest one to a line.
export function CheckBlock({
  text,
  round,
  checks,
  time,
  open = false,
  query = "",
  current = -1,
}: {
  text: string;
  round: number;
  checks: EpicStepCheck[];
  time?: bigint;
  open?: boolean;
  query?: string;
  current?: number;
}): JSX.Element {
  const [shown, setShown] = useState(open);
  const roundChecks = checks.filter((check) => check.round === round);
  const tone: Tone = roundChecks.length === 0 ? "engine" : roundChecks.some((check) => !check.ok) ? "bad" : "ok";
  const { note, rest } = checkLines(text);
  const opens = rest.length > 0;
  const noteDrawn = drawnText(note);
  const restDrawn = drawnText(rest.join("\n"));
  const restCurrent = partCurrent(current, noteDrawn.length + 1, restDrawn.length);
  useReveal(restCurrent >= 0, setShown);
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
      <Markdown text={note} query={query} current={partCurrent(current, 0, noteDrawn.length)} />
      {opens && shown && (
        <div class="opened">
          <Markdown text={rest.join("\n")} query={query} current={restCurrent} />
        </div>
      )}
    </Block>
  );
}

// EventBlock draws what the engine sent: the gear icon and EVENT from the engine, or for a cron prompt the clock icon and
// CRON, and its time. Its text is Markdown, marked with the find's query as YouBlock's is.
export function EventBlock({
  text,
  time,
  cron = false,
  query = "",
  current = -1,
}: {
  text: string;
  time?: bigint;
  cron?: boolean;
  query?: string;
  current?: number;
}): JSX.Element {
  return (
    <Block
      tone="engine"
      kind="event"
      icon={cron ? "clock" : "gear"}
      label={cron ? "CRON" : "EVENT"}
      words={cron ? undefined : "from the engine"}
      time={time === undefined ? undefined : clock(time)}
    >
      <Markdown text={text} query={query} current={current} />
    </Block>
  );
}
