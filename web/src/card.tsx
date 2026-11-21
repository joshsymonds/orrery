import { toJsonString } from "@bufbuild/protobuf";
import type { JSX } from "preact";
import { useCallback, useEffect, useRef, useState } from "preact/hooks";

import type { Clients } from "./actions";
import { Block } from "./blocks";
import { epicGaugeOf } from "./epicrow";
import type { EpicsView } from "./epics";
import { EpicSchema, type EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import { Gauge, Icon } from "./glyphs";

// EpicCalls are the epic calls the reader's epic card makes. get reads an epic, and answer sends the owner's choice to its
// open question, resolving to "" once it lands. openEpic opens the epic's page, and openStep opens the page of a step the
// epic holds, on the step's worker session. epics is what the page's poll has
// loaded: a card shows the poll's detail of its epic when the poll holds one, and otherwise reads its epic itself, on
// mount and each time the epic's listed message changes. stepResult reads a step's full return, which a step card draws
// in its return fold. sessionTitle names a session by its listed conversation's title, for the lines that name one, and is
// undefined when no listed conversation has that session. now is the app's clock, in unix seconds, which an epic's Ask
// ages its question by.
export interface EpicCalls {
  get: (id: string) => Promise<EpicsGetResponse>;
  answer: (id: string, question: string, choice: string, text: string) => Promise<string>;
  openEpic: (id: string) => void;
  openStep: (epic: string, step: string) => void;
  stepResult: (id: string) => Promise<string>;
  sessionTitle?: (session: string) => string | undefined;
  now: number;
  epics: EpicsView;
}

// The fixed line the card's calls leave when an answer fails. It never carries server text.
const answerFailedText = "no answer was sent";
// createdSentence is the sentence an epic's Created block draws.
const createdSentence = "Created the epic from the accepted contract.";

// epicCallsOf makes the epic calls over the clients, with the app's openers, its clock and the epics its poll has loaded.
export function epicCallsOf(
  clients: Clients,
  openEpic: (id: string) => void,
  openStep: (epic: string, step: string) => void,
  now: number,
  epics: EpicsView,
): EpicCalls {
  return {
    get: (id) => clients.epics.get({ id }),
    answer: (id, question, choice, text) =>
      clients.epics.answer({ epicId: id, id: crypto.randomUUID(), text, question, choice }).then(
        () => "",
        () => answerFailedText,
      ),
    openEpic,
    openStep,
    stepResult: (id) => clients.steps.result({ id }).then((reply) => reply.result),
    now,
    epics,
  };
}

// The reading of an epic: the detail it read, or failed when it could not be read or holds no epic. Null until the
// first read settles.
export type Reading = { detail: EpicsGetResponse } | "failed" | null;

// An epic's own reading, with the poll's detail of the epic when it was asked for, which is undefined when the poll held
// none.
interface OwnReading {
  reading: Reading;
  against: EpicsGetResponse | undefined;
}

// useEpicReading is the reading of epic that a card draws, and reread, which reads the epic once more. While the poll
// holds a detail of the epic, the reading is that detail and nothing is read, except that a reread's answer is drawn
// until the poll's detail changes. While the poll holds none, the epic is read on mount and each time its listed message
// changes, the last reading kept while the next one is in flight. Each read uses the calls as they are when it starts,
// so a new calls object on each render of the reader reads nothing.
export function useEpicReading(epic: string, calls: EpicCalls): { reading: Reading; reread: () => void } {
  const polled = calls.epics.details.get(epic);
  const listed = calls.epics.listed.get(epic);
  const listedKey = listed === undefined ? "" : toJsonString(EpicSchema, listed);
  const polledHeld = polled !== undefined;
  const [own, setOwn] = useState<OwnReading | null>(null);
  const [rereads, setRereads] = useState(0);
  const latest = useRef(calls);
  latest.current = calls;
  const polledNow = useRef(polled);
  polledNow.current = polled;
  // Whether the next run of the read effect was asked for by reread, which reads even while the poll holds a detail.
  const rereadAsked = useRef(false);
  useEffect(() => {
    const asked = rereadAsked.current;
    rereadAsked.current = false;
    if (polledHeld && !asked) {
      return;
    }
    let current = true;
    const against = polledNow.current;
    latest.current.get(epic).then(
      (detail) => {
        if (current) {
          setOwn({ reading: detail.epic === undefined ? "failed" : { detail }, against });
        }
      },
      () => {
        if (current) {
          setOwn({ reading: "failed", against });
        }
      },
    );
    return () => {
      current = false;
    };
  }, [epic, polledHeld, polledHeld ? "" : listedKey, rereads]);
  const reread = useCallback(() => {
    rereadAsked.current = true;
    setRereads((count) => count + 1);
  }, []);
  return { reading: shownReading(own, polled), reread };
}

// shownReading is the reading a card draws: the epic's own reading while the poll holds no detail of it, else the own
// reading a reread asked for against the poll's current detail, when it read the epic, else the poll's detail.
function shownReading(own: OwnReading | null, polled: EpicsGetResponse | undefined): Reading {
  if (polled === undefined) {
    return own?.reading ?? null;
  }
  if (own !== null && own.against === polled && own.reading !== null && own.reading !== "failed") {
    return own.reading;
  }
  return { detail: polled };
}

// EpicWidget draws the epic a tool call created as an EpicEventBlock Created: the engine tone, led by the epic's gauge as the
// epic list draws it, the sentence the epic was created with, and the epic's slug as a link that opens it. It reads the
// epic as useEpicReading reads it; an epic that cannot be read draws the block with no gauge.
export function EpicWidget({ epic, calls }: { epic: string; calls: EpicCalls }): JSX.Element {
  const { reading } = useEpicReading(epic, calls);
  const gauge =
    reading !== null && reading !== "failed"
      ? epicGaugeOf(calls.epics.listed.get(epic) ?? reading.detail.epic, reading.detail, calls.now)
      : null;
  return (
    <div class="epic-card">
      <Block
        tone="engine"
        kind="epicevent"
        icon="plus"
        label="EPIC"
        gauge={gauge === null ? undefined : <Gauge {...gauge} />}
        end={
          <button type="button" class="epic-link" onClick={() => calls.openEpic(epic)}>
            <span class="slug">{slugOf(epic)}</span>
            <Icon name="chevronRight" />
          </button>
        }
      >
        <p class="ring-sentence">{createdSentence}</p>
      </Block>
    </div>
  );
}

// slugOf is an epic's slug: the part of its id after the slash, which is the whole id when it has no slash.
function slugOf(id: string): string {
  return id.slice(id.indexOf("/") + 1);
}
