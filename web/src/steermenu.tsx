import type { JSX } from "preact";
import { useEffect, useRef, useState } from "preact/hooks";

import type { Row } from "./rows";
import { type SteerCalls, steerChoices, steerFailureOf } from "./steer";
import { lastPathElement } from "./titleparts";

// SteerState is where a steering move stands: ready for a choice, sending its call, refused with orreryd's reason, or
// unanswered, when the call may have gone through and the epic is being read again.
export type SteerState =
  | { kind: "ready" }
  | { kind: "sending" }
  | { kind: "refused"; reason: string }
  | { kind: "unanswered" };

// SteerHandle is what a host holds for one epic's steering: the state, the steering the latest re-read named, and choose.
export interface SteerHandle {
  state: SteerState;
  steering: string | null;
  choose: (session: string | null) => void;
}

// The note an unanswered move shows while the epic is read again.
const unansweredText = "The server did not answer, so the move may have gone through. Reading the epic again…";

// useSteer makes one steering call for an epic at a time. choose sends a move to a session, or a continue when it is given
// null, and a choice while a call is sending or unanswered sends nothing. An answered call reads the epic back and steers
// to the session that read names. A refused call shows its reason and sends nothing more. An unanswered call reads the epic
// back once, and its rows return with the steering that read names, without steering.
export function useSteer(epic: string, calls: SteerCalls, onSteered: (session: string) => void): SteerHandle {
  const [state, setState] = useState<SteerState>({ kind: "ready" });
  const [steering, setSteering] = useState<string | null>(null);
  // The state as the last set, so a choice made before the next render sees the call still in flight.
  const latest = useRef<SteerState>({ kind: "ready" });
  // Whether the host is still mounted, so an answer that arrives after it unmounts changes nothing.
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  const set = (next: SteerState) => {
    latest.current = next;
    setState(next);
  };

  // readBack reads the epic after a call that answered, and steers to the session it names. When the read fails, it steers to
  // the session the call chose, when that is not empty.
  const readBack = (chosen: string) => {
    calls.get(epic).then(
      (detail) => {
        if (!mounted.current) {
          return;
        }
        const named = detail.epic?.steering ?? "";
        setSteering(named);
        set({ kind: "ready" });
        if (named !== "") {
          onSteered(named);
        }
      },
      () => {
        if (!mounted.current) {
          return;
        }
        set({ kind: "ready" });
        if (chosen !== "") {
          onSteered(chosen);
        }
      },
    );
  };

  // failed shows a refusal, or for a call that gave no answer reads the epic back once and keeps the rows disabled if that
  // read fails too.
  const failed = (error: unknown) => {
    if (!mounted.current) {
      return;
    }
    const failure = steerFailureOf(error);
    if (failure.kind === "refused") {
      set({ kind: "refused", reason: failure.reason });
      return;
    }
    set({ kind: "unanswered" });
    calls.get(epic).then(
      (detail) => {
        if (!mounted.current) {
          return;
        }
        setSteering(detail.epic?.steering ?? "");
        set({ kind: "ready" });
      },
      () => undefined,
    );
  };

  const choose = (session: string | null) => {
    if (latest.current.kind === "sending" || latest.current.kind === "unanswered") {
      return;
    }
    set({ kind: "sending" });
    const answered = session === null ? calls.continue(epic) : calls.move(epic, session).then(() => session);
    answered.then(readBack, failed);
  };

  return { state, steering, choose };
}

// SteerMenu is the list of conversations an epic can be steered from: its current steerer, marked and not choosable, the
// project's other live conversations, and a new conversation. A choice sends through the handle, and while a call runs or
// waits for its answer every row is disabled. Escape closes it through onClose and stops there.
export function SteerMenu({
  slug,
  steering,
  repository,
  rows,
  steer,
  onClose,
}: {
  slug: string;
  steering: string;
  repository: string;
  rows: Row[];
  steer: SteerHandle;
  onClose: () => void;
}): JSX.Element {
  const busy = steer.state.kind === "sending" || steer.state.kind === "unanswered";
  const { current, others } = steerChoices(rows, steer.steering ?? steering, repository);
  const onKeyDown = (event: KeyboardEvent) => {
    if (event.key === "Escape") {
      event.stopPropagation();
      onClose();
    }
  };
  return (
    <div class="rpick steer-menu" role="menu" aria-label={`Steer ${slug} from`} onKeyDown={onKeyDown}>
      <p class="menu-head">Steer {slug} from</p>
      {current !== null && (
        <button
          type="button"
          role="menuitem"
          class="rpick-row steer-row current"
          disabled
          aria-current="true"
          data-session={current.archiveId}
        >
          <span class="t">{current.label}</span>
          <span class="s">steers it now</span>
        </button>
      )}
      {others.map((row) => (
        <button
          key={row.key}
          type="button"
          role="menuitem"
          class="rpick-row steer-row"
          data-session={row.archiveId}
          disabled={busy}
          onClick={() => steer.choose(row.archiveId)}
        >
          <span class="t">{row.label}</span>
          <span class="s">{`${lastPathElement(row.project)} · ${row.state}`}</span>
        </button>
      ))}
      <hr />
      <button
        type="button"
        role="menuitem"
        class="rpick-row steer-row new"
        disabled={busy}
        onClick={() => steer.choose(null)}
      >
        <span class="t">A new conversation in {lastPathElement(repository)}</span>
        <span class="s">starts with the contract and the epic’s log</span>
      </button>
      {steer.state.kind === "refused" && <p class="steer-note error">{steer.state.reason}</p>}
      {steer.state.kind === "unanswered" && <p class="steer-note error">{unansweredText}</p>}
    </div>
  );
}
