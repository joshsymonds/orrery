import type { JSX } from "preact";
import { useRef, useState } from "preact/hooks";

import { Ask, AskLine } from "./ask";
import { epicAsk, epicProject } from "./askmodel";
import { Block } from "./blocks";
import { type EpicCalls, useEpicReading } from "./card";
import type { StepFacts } from "./gen/orrery/v1/orrery_pb";
import { Icon, type IconName } from "./glyphs";
import { clock } from "./reader";

// The kinds an epic's event names, as its orrery-event block gives them.
const eventKinds = ["question", "park", "release", "end", "moved"] as const;
type EventKind = (typeof eventKinds)[number];

// stepKind is the kind a step's end names, as enginehost.CallerEventBlock writes it.
const stepKind = "step_end";

// EpicEvent is an event an epic sent its creator, read from its block: its kind, the epic it is about, the tail of its
// event id (the question's id, or the phase the epic reached), and the first sentence of its body. A moved event has no
// sentence; its moved side says what the line draws. On the side the epic left, to is the session it moved to and by is
// the mover as the body names it: the owner, a session, or orreryd.
export interface EpicEvent {
  kind: EventKind;
  epic: string;
  tail: string;
  sentence: string;
  moved?: { side: "left"; to: string; by: string } | { side: "took" };
}

// StepEvent is a step's end: the step's id, the agent that ran it (empty when the block names none), how it ended, the
// epic that holds it and the task it ran (each empty when the block names none), and the brief the block gives, one
// string a line.
export interface StepEvent {
  step: string;
  agent: string;
  end: string;
  epic: string;
  task: string;
  brief: string[];
}

// blockPattern matches a block as enginehost.CallerEventBlock writes it: the opening tag with its kind and id first and
// any further attributes after them, the body on lines of its own, and the closing tag. A value holds no raw double
// quote, since the daemon escapes it.
const blockPattern = /^<orrery-event kind="([^"]*)" id="([^"]*)"((?: [a-z_]+="[^"]*")*)>\n([\s\S]*)\n<\/orrery-event>$/;
// attributePattern matches one attribute after the id in an opening tag.
const attributePattern = / ([a-z_]+)="([^"]*)"/g;
// entities are the escapes the daemon writes into an attribute value and a body.
const entities: Record<string, string> = { amp: "&", lt: "<", gt: ">", quot: '"' };
const epicPrefix = "epic:";
const stepPrefix = "step:";
// morePrefix starts a block's last line when that line points to the full return, which is not part of the brief.
const morePrefix = "More: ";
// leftPattern matches the first line of the body a move gives the conversation the epic left.
const leftPattern = /^Epic [^\n]* moved to session (\S+), by (the owner|orreryd|session \S+)\.(?=\n|$)/;
const glyphs: Record<EventKind, string> = { question: "?", park: "‖", release: "↗", end: "■", moved: "⇄" };
const labels: Record<EventKind, string> = {
  question: "question",
  park: "parked",
  release: "released",
  end: "ended",
  moved: "moved",
};

// Block is an event block as its text holds it: its kind and id, its attributes after them, and its body, with every
// attribute value and the body unescaped.
interface Block {
  kind: string;
  id: string;
  attributes: Map<string, string>;
  body: string;
}

// blockOf reads text as an event block, or returns null for any other text.
function blockOf(text: string): Block | null {
  const match = blockPattern.exec(text.trim());
  if (match === null) {
    return null;
  }
  const [, kind, id, further, body] = match;
  const attributes = new Map<string, string>();
  for (const [, name, value] of further.matchAll(attributePattern)) {
    attributes.set(name, unescapeText(value));
  }
  return { kind: unescapeText(kind), id: unescapeText(id), attributes, body: unescapeText(body) };
}

// unescapeText reverses the daemon's escapes in one pass, so &amp;lt; reads as &lt; and not as <.
function unescapeText(text: string): string {
  return text.replace(/&(amp|lt|gt|quot);/g, (_, name: string) => entities[name]);
}

// epicEventOf reads a prompt's text as an epic's event: a block of one of the event kinds whose id is
// epic:<epic>:<tail>. Its first sentence is the body's, and a moved event's side is read from its body instead. It is
// null for any other text, a step's event among them.
export function epicEventOf(text: string): EpicEvent | null {
  const block = blockOf(text);
  if (block === null) {
    return null;
  }
  const { kind, id, body } = block;
  if (!isEventKind(kind) || !id.startsWith(epicPrefix)) {
    return null;
  }
  const rest = id.slice(epicPrefix.length);
  const cut = rest.lastIndexOf(":");
  if (cut <= 0 || cut === rest.length - 1) {
    return null;
  }
  const epic = rest.slice(0, cut);
  const tail = rest.slice(cut + 1);
  if (kind === "moved") {
    return { kind, epic, tail, sentence: "", moved: movedOf(body) };
  }
  return { kind, epic, tail, sentence: firstSentence(body) };
}

// movedOf is a move's side from its body: left when the body is the one the conversation the epic left gets, and took
// for any other body, which is the steering prompt the conversation that took the epic gets.
function movedOf(body: string): NonNullable<EpicEvent["moved"]> {
  const match = leftPattern.exec(body);
  if (match === null) {
    return { side: "took" };
  }
  return { side: "left", to: match[1], by: match[2] };
}

// stepEventOf reads a prompt's text as a step's end: a block of kind step_end whose id is step:<id>, with end and
// status attributes, and with agent, epic and task attributes when it names them. Its brief is the body's lines,
// without a last line that starts with More:. A block with no end or no status is no step's end, so an older block
// reads as no step's end. It is null for any other text.
export function stepEventOf(text: string): StepEvent | null {
  const block = blockOf(text);
  if (block === null || block.kind !== stepKind || !block.id.startsWith(stepPrefix)) {
    return null;
  }
  const { attributes } = block;
  const end = attributes.get("end");
  if (end === undefined || !attributes.has("status")) {
    return null;
  }
  return {
    step: block.id.slice(stepPrefix.length),
    agent: attributes.get("agent") ?? "",
    end,
    epic: attributes.get("epic") ?? "",
    task: attributes.get("task") ?? "",
    brief: briefOf(block.body),
  };
}

// briefOf is a step's brief from its body: the body's lines, without a last line that starts with More:. An empty body
// has no lines.
function briefOf(body: string): string[] {
  return body === "" ? [] : dropMore(body.split("\n"));
}

// dropMore returns lines without a last line that starts with More:, the line that points to the full return.
function dropMore(lines: string[]): string[] {
  return lines.at(-1)?.startsWith(morePrefix) === true ? lines.slice(0, -1) : lines;
}

// returnBody is a return as the fold draws it: pretty-printed when the return parses as JSON, its raw text otherwise.
function returnBody(result: string): string {
  let value: unknown;
  try {
    value = JSON.parse(result);
  } catch {
    return result;
  }
  return JSON.stringify(value, null, 2);
}

function isEventKind(kind: string): kind is EventKind {
  return (eventKinds as readonly string[]).includes(kind);
}

// firstSentence is the body's first sentence: its text up to a full stop that ends a word, or else its first line.
function firstSentence(body: string): string {
  const match = /^[^\n]*?\.(?=\s|$)|^[^\n]*/.exec(body.trimStart());
  return match === null ? "" : match[0].trim();
}

// EventRing draws an epic's event as one ringed line: an icon for its kind, the event's sentence and the epic's slug. A
// click opens the epic. A question event also draws its question under the line, through QuestionReading.
export function EventRing({ event, calls }: { event: EpicEvent; calls: EpicCalls }): JSX.Element {
  return (
    <div class="event-ring">
      <button type="button" class="ring-line" onClick={() => calls.openEpic(event.epic)}>
        <RingParts kind={event.kind} sentence={sentenceOf(event, calls)} epic={event.epic} />
      </button>
      {event.kind === "question" && <QuestionReading epic={event.epic} question={event.tail} calls={calls} />}
    </div>
  );
}

// sentenceOf is the line's sentence: a moved event's words, which name its epic, where it went and who moved it through
// the calls' session titles, and any other event's first sentence.
function sentenceOf(event: EpicEvent, calls: EpicCalls): string {
  if (event.moved === undefined) {
    return event.sentence;
  }
  const slug = slugOf(event.epic);
  if (event.moved.side === "took") {
    return `Took over ${slug}. Its questions and its ending come here now.`;
  }
  const { to, by } = event.moved;
  return `${slug} moved to ${sessionNameOf(to, calls)}, by ${moverNameOf(by, calls)}. Its questions go there now.`;
}

// moverNameOf names a move's mover as its line draws it: you for the owner, orreryd for orreryd, and the session's name
// for a session the body names.
function moverNameOf(by: string, calls: EpicCalls): string {
  if (by === "the owner") {
    return "you";
  }
  if (by === "orreryd") {
    return by;
  }
  return sessionNameOf(by.slice("session ".length), calls);
}

// sessionNameOf names a session as its line draws it: its listed conversation's title in quotes, or its id when no
// listed conversation has that session.
function sessionNameOf(session: string, calls: EpicCalls): string {
  const title = calls.sessionTitle?.(session);
  return title === undefined ? `session ${session}` : `“${title}”`;
}

// RingParts draws the parts of an epic's event line: its kind's icon, the sentence, and the epic's slug.
function RingParts({ kind, sentence, epic }: { kind: EventKind; sentence: string; epic: string }): JSX.Element {
  return (
    <>
      <span class={`ring-icon ${kind}`} role="img" aria-label={labels[kind]}>
        {glyphs[kind]}
      </span>
      <span class="ring-sentence">{sentence}</span>
      <span class="ring-slug">{slugOf(epic)}</span>
    </>
  );
}

// ReturnLoad is a step's full return as its fold holds it: the return once it loads, failed when it did not load, and
// null until the fold is first opened.
type ReturnLoad = { result: string } | "failed" | null;

// endWords is each step end's chip, in round 6's words. An end it does not name draws as it is written.
const endWords = new Map<string, string>([
  ["done", "done"],
  ["returned", "returned"],
  ["failed", "failed"],
  ["goal_unmet", "goal not met"],
  ["checks_failed", "checks failed"],
  ["gate_failed", "gate failed"],
  ["stopped", "stopped"],
  ["engine_restarted", "engine restarted"],
  ["overtime", "ran over time"],
  ["tool_stuck", "a tool stuck"],
  ["silent", "went silent"],
  ["looping", "looping"],
  ["wedged", "wedged"],
]);

// endVerdictOf is a step end's tone and icon: done and returned pass, stopped is quiet, and every other end failed.
function endVerdictOf(end: string): { tone: "ok" | "quiet" | "bad"; icon: IconName } {
  if (end === "done" || end === "returned") {
    return { tone: "ok", icon: "check" };
  }
  if (end === "stopped") {
    return { tone: "quiet", icon: "stop" };
  }
  return { tone: "bad", icon: "cross" };
}

// durationOf is a run's length: seconds under a minute, minutes under an hour, then hours and minutes.
function durationOf(seconds: number): string {
  if (seconds < 60) {
    return `${seconds}s`;
  }
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) {
    return `${minutes}m`;
  }
  return `${Math.floor(minutes / 60)}h ${minutes % 60}m`;
}

// plural is a count and its word, in the singular for one and the plural otherwise.
function plural(count: number, word: string): string {
  return count === 1 ? `1 ${word}` : `${count} ${word}s`;
}

// FactsLine draws a step end's facts: its files and line counts when they were counted, its rounds when above 0, and
// how long it ran.
function FactsLine({ facts }: { facts: StepFacts }): JSX.Element {
  return (
    <div class="facts">
      {facts.counted && (
        <>
          <span>{plural(Number(facts.files), "file")}</span>
          <span class="plus">{`+${facts.added}`}</span>
          <span class="minus">{`−${facts.removed}`}</span>
        </>
      )}
      {facts.rounds > 0 && <span>{plural(facts.rounds, "round")}</span>}
      <span>{durationOf(Number(facts.seconds))}</span>
    </div>
  );
}

// StepCard draws a step's end as a StepEndBlock: its tone and icon are its verdict, its label says the step ended, and
// it names the task the step ran, else the step's id, with the agent beside it when the block names one. Its end is a
// chip. Its body is each line of the brief as a line of text, then, with epic calls, a return fold closed until it is
// opened, then the facts line when the event carries facts, and the time when one is given. The first time the fold is
// opened, the step's full return is read through calls.stepResult. It draws pretty-printed when it is JSON, and a read
// that fails draws a fixed line. The chip is a button that opens the step's page through calls.openStep when the event
// names its epic and the reader holds epic calls.
export function StepCard({
  event,
  facts,
  time,
  calls,
}: {
  event: StepEvent;
  facts?: StepFacts;
  time?: bigint;
  calls?: EpicCalls;
}): JSX.Element {
  const [loaded, setLoaded] = useState<ReturnLoad>(null);
  const asked = useRef(false);
  const onClick = () => {
    if (calls !== undefined && event.epic !== "") {
      calls.openStep(event.epic, event.step);
    }
  };
  const openReturn = (details: HTMLDetailsElement) => {
    if (calls === undefined || asked.current || !details.open) {
      return;
    }
    asked.current = true;
    calls.stepResult(event.step).then(
      (result) => setLoaded({ result }),
      () => setLoaded("failed"),
    );
  };
  const verdict = endVerdictOf(event.end);
  const chip = <span class={`chip ${verdict.tone}`}>{endWords.get(event.end) ?? event.end}</span>;
  const opens = calls !== undefined && event.epic !== "";
  return (
    <div class="step-card">
      <Block
        tone={verdict.tone}
        kind="stepend"
        icon={verdict.icon}
        label="STEP ENDED"
        who={event.task !== "" ? event.task : event.step}
        words={event.agent !== "" ? event.agent : undefined}
        time={time === undefined ? undefined : clock(time)}
        end={
          opens ? (
            <button type="button" class="step-head" onClick={onClick}>
              {chip}
              <Icon name="chevronRight" />
            </button>
          ) : (
            chip
          )
        }
      >
        {event.brief.map((line, index) => (
          <p key={index} class="step-summary">
            {line}
          </p>
        ))}
        {calls !== undefined && (
          <details class="step-return" onToggle={(toggled) => openReturn(toggled.currentTarget)}>
            <summary>return</summary>
            {loaded === "failed" && <p class="error">return could not be loaded</p>}
            {loaded !== null && loaded !== "failed" && <pre>{returnBody(loaded.result)}</pre>}
          </details>
        )}
        {facts !== undefined && <FactsLine facts={facts} />}
      </Block>
    </div>
  );
}

// QuestionReading draws the question's epic as useEpicReading reads it. While the epic is unread or cannot be read it
// draws nothing. While the question is open it draws the epic's Ask, which takes no number keys and draws no key caps,
// and an answer goes to epic answer for the epic and question; an answer that lands reads the epic once more. Once the
// question is no longer open it draws the settled line, with the epic's project and slug.
function QuestionReading({ epic, question, calls }: { epic: string; question: string; calls: EpicCalls }) {
  const { reading, reread } = useEpicReading(epic, calls);

  if (reading === null || reading === "failed" || reading.detail.epic === undefined) {
    return null;
  }
  const owner = reading.detail.epic;
  const open = reading.detail.questions.find((candidate) => candidate.id === question);
  if (open === undefined) {
    return <AskLine state="settled" who={`${epicProject(owner)} · ${owner.slug}`} />;
  }
  // The Ask is drawn in the now-block column.
  return (
    <div class="now-block">
      <Ask
        key={question}
        model={epicAsk(owner, open)}
        now={calls.now}
        phase="open"
        picked={null}
        note=""
        keyCaps={false}
        narrow={false}
        numberKeys={false}
        onConfirm={(choice, text) =>
          calls.answer(epic, question, choice, text).then((failure) => {
            if (failure === "") {
              reread();
            }
            return failure;
          })
        }
      />
    </div>
  );
}

// slugOf is an epic's slug: the part of its id after the slash, which is the whole id when it has no slash.
function slugOf(id: string): string {
  return id.slice(id.indexOf("/") + 1);
}
