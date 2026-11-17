import type { JSX } from "preact";
import { useEffect, useRef, useState } from "preact/hooks";

import { QuestionCard } from "./blocker";
import type { EpicCalls } from "./card";
import type { EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
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

// StepEvent is a step's end: the step's id, the agent that ran it (empty when the body names none), how it ended, the
// epic that holds it (empty when none does), and its summary as the body gives it. A pending step_end has these fields
// too.
export interface StepEvent {
  step: string;
  agent: string;
  end: string;
  status: string;
  reason: string;
  epic: string;
  summary: string;
}

// blockPattern matches a block as enginehost.CallerEventBlock writes it: the opening tag, the body on lines of its own,
// and the closing tag.
const blockPattern = /^<orrery-event kind="([^"]*)" id="([^"]*)">\n([\s\S]*)\n<\/orrery-event>$/;
const epicPrefix = "epic:";
const stepPrefix = "step:";
// stepLine matches a step's first line as enginehost's stepEndBody writes it, and takes the end, status and reason.
const stepLine = /^Step \S+ ended: end (.*?), status (.*?), reason (.*)\.$/;
const agentLine = "Agent: ";
const epicLine = "Epic: ";
const summaryLine = "Summary: ";
// stepHint is a step body's last line, which points to the full result. It is not part of the summary.
const stepHint = "step_status gives the full result.";
const glyphs: Record<EventKind, string> = { question: "?", park: "‖", release: "↗", end: "■" };
const labels: Record<EventKind, string> = { question: "question", park: "parked", release: "released", end: "ended" };

// epicEventOf reads a prompt's text as an epic's event: a block of one of the four kinds whose id is epic:<epic>:<tail>.
// It is null for any other text, a step's event among them.
export function epicEventOf(text: string): EpicEvent | null {
  const match = blockPattern.exec(text.trim());
  if (match === null) {
    return null;
  }
  const [, kind, id, body] = match;
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

// stepEventOf reads a prompt's text as a step's end: a block of kind step_end whose id is step:<id>, with its first line,
// an Agent line and an Epic line when the body has them, and a Summary line. A body with no Agent line reads with no
// agent. It is null for any other text.
export function stepEventOf(text: string): StepEvent | null {
  const match = blockPattern.exec(text.trim());
  if (match === null) {
    return null;
  }
  const [, kind, id, body] = match;
  if (kind !== stepKind || !id.startsWith(stepPrefix)) {
    return null;
  }
  const [first, ...lines] = body.split("\n");
  const head = stepLine.exec(first);
  if (head === null) {
    return null;
  }
  const lineAt = (index: number): string => lines[index] ?? "";
  const [, end, status, reason] = head;
  let at = 0;
  let agent = "";
  let epic = "";
  if (lineAt(at).startsWith(agentLine)) {
    agent = lineAt(at).slice(agentLine.length);
    at += 1;
  }
  if (lineAt(at).startsWith(epicLine)) {
    epic = lineAt(at).slice(epicLine.length);
    at += 1;
  }
  if (!lineAt(at).startsWith(summaryLine)) {
    return null;
  }
  const summary = [lineAt(at).slice(summaryLine.length), ...lines.slice(at + 1)];
  if (summary.at(-1) === stepHint) {
    summary.pop();
  }
  return { step: id.slice(stepPrefix.length), agent, end, status, reason, epic, summary: summary.join("\n") };
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

// StepCard draws a step's end as a card: the agent when the event names one, the end, status and reason, and the first
// line of the summary. A click opens the step's page through calls.openStep when the event names its epic and the reader
// holds epic calls. Any other click expands the card in place to the whole summary, and it stays expanded.
// waiting, when set, dims the card and labels it, as a waiting card does.
export function StepCard({
  event,
  calls,
  waiting = "",
}: {
  event: StepEvent;
  calls?: EpicCalls;
  waiting?: string;
}): JSX.Element {
  const [expanded, setExpanded] = useState(false);
  const opens = calls !== undefined && event.epic !== "";
  const onClick = () => {
    if (calls !== undefined && event.epic !== "") {
      calls.openStep(event.epic, event.step);
      return;
    }
    setExpanded(true);
  };
  const [firstLine] = event.summary.split("\n");
  return (
    <div class={waiting === "" ? "step-card" : "step-card waiting"}>
      <button type="button" class="step-head" aria-expanded={opens ? undefined : expanded} onClick={onClick}>
        {event.agent !== "" && <b class="step-agent">{event.agent}</b>}
        <span class="step-end">end {event.end}</span>
        <span class="step-status">status {event.status}</span>
        {event.reason !== "" && <span class="step-reason">reason {event.reason}</span>}
      </button>
      {event.summary !== "" && <p class="step-summary">{expanded ? event.summary : firstLine}</p>}
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
    return <StepCard event={event} calls={calls} waiting={label} />;
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

// The reading of the question's epic: the detail it read, or failed when it could not be read or holds no epic. Null
// until the first read settles.
type Reading = { detail: EpicsGetResponse } | "failed" | null;

// QuestionReading reads the question's epic on mount and each time calls.tick changes, keeping the last reading while the
// next is in flight. While the epic is unread or cannot be read it draws nothing. While the question is open it draws the
// answer form the epic page draws, with no number keys, and an answer goes to epic answer for the epic and question. Once
// the question is no longer open it draws a line saying so.
function QuestionReading({ epic, question, calls }: { epic: string; question: string; calls: EpicCalls }) {
  const [reading, setReading] = useState<Reading>(null);
  // The effect below runs only when the epic or the tick changes, and reads the calls as they are then. A new calls
  // object on each render of the reader does not read the epic again.
  const latest = useRef(calls);
  latest.current = calls;
  const { tick } = calls;
  useEffect(() => {
    let current = true;
    latest.current.get(epic).then(
      (detail) => {
        if (current) {
          setReading(detail.epic === undefined ? "failed" : { detail });
        }
      },
      () => {
        if (current) {
          setReading("failed");
        }
      },
    );
    return () => {
      current = false;
    };
  }, [epic, tick]);

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
        onAnswer={(questionId, choice, text) => calls.answer(epic, questionId, choice, text)}
      />
    </div>
  );
}

// slugOf is an epic's slug: the part of its id after the slash, which is the whole id when it has no slash.
function slugOf(id: string): string {
  return id.slice(id.indexOf("/") + 1);
}
