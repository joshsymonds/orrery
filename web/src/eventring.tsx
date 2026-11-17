import type { JSX } from "preact";
import { useEffect, useRef, useState } from "preact/hooks";

import { QuestionCard } from "./blocker";
import type { EpicCalls } from "./card";
import type { EpicsGetResponse } from "./gen/orrery/v1/epics_pb";

// The kinds an epic's event names, as its orrery-event block gives them.
const eventKinds = ["question", "park", "release", "end"] as const;
type EventKind = (typeof eventKinds)[number];

// EpicEvent is an event an epic sent its creator, read from its block: its kind, the epic it is about, the tail of its
// event id (the question's id, or the phase the epic reached), and the first sentence of its body.
export interface EpicEvent {
  kind: EventKind;
  epic: string;
  tail: string;
  sentence: string;
}

// blockPattern matches a block as enginehost.CallerEventBlock writes it: the opening tag, the body on lines of its own,
// and the closing tag.
const blockPattern = /^<orrery-event kind="([^"]*)" id="([^"]*)">\n([\s\S]*)\n<\/orrery-event>$/;
const epicPrefix = "epic:";
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
        <span class={`ring-icon ${event.kind}`} role="img" aria-label={labels[event.kind]}>
          {glyphs[event.kind]}
        </span>
        <span class="ring-sentence">{event.sentence}</span>
        <span class="ring-slug">{slugOf(event.epic)}</span>
      </button>
      {event.kind === "question" && <QuestionReading epic={event.epic} question={event.tail} calls={calls} />}
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
