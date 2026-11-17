import type { JSX } from "preact";
import { useEffect, useRef, useState } from "preact/hooks";

import type { Clients } from "./actions";
import { nowSentence } from "./blocker";
import type { EpicRequirement, EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import { provenOf, type Wedge, wedgesOf } from "./phases";
import { controlsOf, trackPart, trackParts } from "./status";

// EpicCalls are the epic calls the reader's epic card makes. get reads an epic, pause and resume command it, and answer
// sends the owner's choice to its open question, resolving to "" once it lands. openEpic opens the epic's page. tick is
// the poll's clock: the card reads its epic again each time tick changes.
export interface EpicCalls {
  get: (id: string) => Promise<EpicsGetResponse>;
  pause: (id: string) => Promise<unknown>;
  resume: (id: string) => Promise<unknown>;
  answer: (id: string, question: string, choice: string, text: string) => Promise<string>;
  openEpic: (id: string) => void;
  tick: number;
}

// The fixed lines the card and its calls leave when a command or an answer fails. They never carry server text.
const commandFailedText = "epic command failed";
const answerFailedText = "no answer was sent";

// epicCallsOf makes the epic calls over the clients, with the app's opener and clock.
export function epicCallsOf(clients: Clients, openEpic: (id: string) => void, tick: number): EpicCalls {
  return {
    get: (id) => clients.epics.get({ id }),
    pause: (id) => clients.epics.pause({ id }),
    resume: (id) => clients.epics.resume({ id }),
    answer: (id, question, choice, text) =>
      clients.epics.answer({ epicId: id, id: crypto.randomUUID(), text, question, choice }).then(
        () => "",
        () => answerFailedText,
      ),
    openEpic,
    tick,
  };
}

// The reading of the card's epic: the detail it read, or failed when it could not be read or holds no epic. Null until
// the first read settles.
type Reading = { detail: EpicsGetResponse } | "failed" | null;

// EpicWidget draws the live card of the epic a tool call created: its head with the slug, the status pill and the open
// control, its five-part track, the now sentence, each requirement as a row, the open question's title when there is
// one, and the command controlsOf shows. It reads the epic on mount and each time calls.tick changes, keeping the last
// reading while the next one is in flight. An epic that cannot be read shows its slug and not available.
export function EpicWidget({ epic, calls }: { epic: string; calls: EpicCalls }): JSX.Element {
  const [reading, setReading] = useState<Reading>(null);
  const [commandError, setCommandError] = useState("");
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

  // command sends a pause or a resume: a landed one clears the line, and a failed one shows its fixed line.
  const command = (sent: Promise<unknown>): void => {
    void sent.then(
      () => setCommandError(""),
      () => setCommandError(commandFailedText),
    );
  };

  const detail = reading !== null && reading !== "failed" ? reading.detail : null;
  return (
    <div class="epic-card">
      <header class="card-head">
        <b class="slug">{slugOf(epic)}</b>
        {detail !== null && <StatusPill detail={detail} />}
        <button type="button" class="open-epic" onClick={() => calls.openEpic(epic)}>
          open the epic ›
        </button>
      </header>
      {reading === "failed" && <p class="unavailable">not available</p>}
      {detail !== null && (
        <EpicBody detail={detail} epic={epic} calls={calls} command={command} commandError={commandError} />
      )}
    </div>
  );
}

// StatusPill draws the card's status pill: paused, then parked, then the track part's name, or the phase as written off
// the track. Paused reads idle, parked waiting, and every other word working.
function StatusPill({ detail }: { detail: EpicsGetResponse }): JSX.Element {
  const tone = detail.paused ? "idle" : detail.parked !== "" ? "waiting" : "working";
  return <span class={`pill tone-${tone}`}>{pillWord(detail)}</span>;
}

// pillWord is the status pill's word: paused, then parked, then the track part's name, or the phase as written when its
// phase is off the track.
function pillWord(detail: EpicsGetResponse): string {
  if (detail.paused) {
    return "paused";
  }
  if (detail.parked !== "") {
    return "parked";
  }
  return trackPart(detail) ?? detail.epic?.outcome ?? "";
}

// EpicBody draws the card under its head: the track, the now sentence, the requirements with their count, the open
// question's title, and the Pause or Resume command with its fixed line when it fails.
function EpicBody({
  detail,
  epic,
  calls,
  command,
  commandError,
}: {
  detail: EpicsGetResponse;
  epic: string;
  calls: EpicCalls;
  command: (sent: Promise<unknown>) => void;
  commandError: string;
}): JSX.Element {
  const wedges = wedgesOf(detail);
  const sentence = nowSentence(wedges);
  const controls = controlsOf(detail);
  const [question] = detail.questions;
  return (
    <>
      <Track detail={detail} />
      {sentence !== "" && <p class="now-sentence">{sentence}</p>}
      <Requirements requirements={detail.requirements} wedges={wedges} />
      {question !== undefined && <p class="question">{question.text}</p>}
      <div class="card-controls">
        {controls.resume && (
          <button type="button" class="act" onClick={() => command(calls.resume(epic))}>
            Resume
          </button>
        )}
        {controls.pause && (
          <button type="button" class="act" onClick={() => command(calls.pause(epic))}>
            Pause
          </button>
        )}
      </div>
      {commandError !== "" && <p class="error">{commandError}</p>}
    </>
  );
}

// Track draws the five parts of the epic's track in order. Contract is always done; a part before the epic's part is
// done, the part itself is now, and the parts after it are ahead. An epic whose phase the track does not name has no
// now part.
function Track({ detail }: { detail: EpicsGetResponse }): JSX.Element {
  const current = trackPart(detail);
  const index = current === undefined ? -1 : trackParts.indexOf(current);
  return (
    <ol class="track">
      {trackParts.map((part, position) => {
        const state = part === "contract" || position < index ? "done" : position === index ? "now" : "ahead";
        return (
          <li key={part} class={`part ${state}`} aria-current={state === "now" ? "step" : undefined}>
            {part}
          </li>
        );
      })}
    </ol>
  );
}

// Requirements draws each requirement as a row, then the count of proven ones out of all of them. Nothing draws when
// the epic has no requirement.
function Requirements({
  requirements,
  wedges,
}: {
  requirements: EpicRequirement[];
  wedges: Wedge[];
}): JSX.Element | null {
  if (requirements.length === 0) {
    return null;
  }
  const rows = requirements.map((requirement) => ({
    requirement,
    proven: provenOf(requirement.id, wedges, requirements),
  }));
  const provenCount = rows.filter((row) => row.proven).length;
  return (
    <>
      <ul class="reqs">
        {rows.map(({ requirement, proven }) => (
          <li key={requirement.id} class={requirementTone(requirement, proven)}>
            <span class="rid">{requirement.id}</span>
            <span class="text">{requirement.text}</span>
          </li>
        ))}
      </ul>
      <p class="count">{`${provenCount} of ${requirements.length} proven`}</p>
    </>
  );
}

// requirementTone is a requirement row's colour: green when proven, red when a red run of it passed, which is its red
// test written and failing, and grey otherwise.
function requirementTone(requirement: EpicRequirement, proven: boolean): "green" | "red" | "grey" {
  if (proven) {
    return "green";
  }
  return requirement.runs.some((run) => run.phase === "red" && run.passed) ? "red" : "grey";
}

// slugOf is an epic's slug: the part of its id after the slash, which is the whole id when it has no slash.
function slugOf(id: string): string {
  return id.slice(id.indexOf("/") + 1);
}
