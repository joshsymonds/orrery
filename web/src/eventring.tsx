import type { JSX } from "preact";
import { useRef, useState } from "preact/hooks";

import { QuestionCard } from "./blocker";
import { type EpicCalls, useEpicReading } from "./card";
import type { PendingEvent } from "./gen/orrery/v1/orrery_pb";

// The kinds an epic's event names, as its orrery-event block gives them.
const eventKinds = ["question", "park", "release", "end"] as const;
type EventKind = (typeof eventKinds)[number];

// stepKind is the kind a step's end names, as enginehost.CallerEventBlock writes it.
const stepKind = "step_end";

// EpicEvent is an event an epic sent its creator, read from its block: its kind, the epic it is about, the tail of its
// event id (the question's id, or the phase the epic reached), and the first sentence of its body.
export interface EpicEvent {
  kind: EventKind;
  epic: string;
  tail: string;
  sentence: string;
}

// StepEvent is a step's end: the step's id, the agent that ran it (empty when the block names none), how it ended and
// its status, the epic that holds it and the task it ran (each empty when the block names none), and the brief the
// block gives, one string a line. A pending step_end has these fields too, its brief its one summary.
export interface StepEvent {
  step: string;
  agent: string;
  end: string;
  status: string;
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
const glyphs: Record<EventKind, string> = { question: "?", park: "‖", release: "↗", end: "■" };
const labels: Record<EventKind, string> = { question: "question", park: "parked", release: "released", end: "ended" };

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

// epicEventOf reads a prompt's text as an epic's event: a block of one of the four kinds whose id is
// epic:<epic>:<tail>. Its first sentence is the body's. It is null for any other text, a step's event among them.
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
  return { kind, epic: rest.slice(0, cut), tail: rest.slice(cut + 1), sentence: firstSentence(body) };
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
  const status = attributes.get("status");
  if (end === undefined || status === undefined) {
    return null;
  }
  return {
    step: block.id.slice(stepPrefix.length),
    agent: attributes.get("agent") ?? "",
    end,
    status,
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

// EventRing draws an epic's event as one ringed line: an icon for its kind, the event's first sentence and the epic's
// slug. A click opens the epic. A question event also draws its question under the line, through QuestionReading.
export function EventRing({ event, calls }: { event: EpicEvent; calls: EpicCalls }): JSX.Element {
  return (
    <div class="event-ring">
      <button type="button" class="ring-line" onClick={() => calls.openEpic(event.epic)}>
        <RingParts kind={event.kind} sentence={event.sentence} epic={event.epic} />
      </button>
      {event.kind === "question" && <QuestionReading epic={event.epic} question={event.tail} calls={calls} />}
    </div>
  );
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

// StepCard draws a step's end as a card: its head with the agent when the event names one, then the end and the
// status, then each line of the brief as a line of text. With epic calls it holds a return fold under the brief, closed
// until it is opened. The first time the fold is opened, the step's full return is read through calls.stepResult. It
// draws pretty-printed when it is JSON, and a read that fails draws a fixed line. A click on the head opens the step's
// page through calls.openStep when the event names its epic and the reader holds epic calls. waiting, when set, dims
// the card and labels it, as a waiting card does.
export function StepCard({
  event,
  calls,
  waiting = "",
}: {
  event: StepEvent;
  calls?: EpicCalls;
  waiting?: string;
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
  return (
    <div class={waiting === "" ? "step-card" : "step-card waiting"}>
      <button type="button" class="step-head" onClick={onClick}>
        {event.agent !== "" && <b class="step-agent">{event.agent}</b>}
        <span class="step-end">end {event.end}</span>
        <span class="step-status">status {event.status}</span>
      </button>
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
      {waiting !== "" && <small class="wait-label">{waiting}</small>}
    </div>
  );
}

// WaitingCard draws a caller event the session holds and has not taken: a step's end as a step card, or an epic's event
// as its line, dimmed and labelled with how it waits. speak waits for the owner to speak; any other value waits for the
// session to be free. A pending event of a kind the reader does not know draws nothing.
export function WaitingCard({ event, calls }: { event: PendingEvent; calls?: EpicCalls }): JSX.Element | null {
  const label = event.waits === "speak" ? "arrives when you next speak" : "arrives when the session is free";
  if (event.kind === stepKind) {
    const step: StepEvent = {
      step: event.step,
      agent: event.agent,
      end: event.end,
      status: event.status,
      epic: event.epic,
      task: "",
      brief: event.brief === "" ? [] : dropMore(event.brief.split("\n")),
    };
    return <StepCard event={step} calls={calls} waiting={label} />;
  }
  const kind = event.kind;
  if (!isEventKind(kind)) {
    return null;
  }
  return (
    <div class="event-ring waiting">
      <div class="ring-line">
        <RingParts kind={kind} sentence={event.title} epic={event.epic} />
      </div>
      <small class="wait-label">{label}</small>
    </div>
  );
}

// QuestionReading draws the question's epic as useEpicReading reads it. While the epic is unread or cannot be read it
// draws nothing. While the question is open it draws the answer form the epic page draws, with no number keys, and an
// answer goes to epic answer for the epic and question; an answer that lands reads the epic once more. Once the question
// is no longer open it draws a line saying so.
function QuestionReading({ epic, question, calls }: { epic: string; question: string; calls: EpicCalls }) {
  const { reading, reread } = useEpicReading(epic, calls);

  if (reading === null || reading === "failed") {
    return null;
  }
  const open = reading.detail.questions.find((candidate) => candidate.id === question);
  if (open === undefined) {
    return <p class="settled">This question is settled.</p>;
  }
  // The form takes the epic page's now-block styles, since its answer rules are scoped to that block.
  return (
    <div class="now-block">
      <QuestionCard
        question={open}
        numberKeys={false}
        onAnswer={(questionId, choice, text) =>
          calls.answer(epic, questionId, choice, text).then((failure) => {
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
