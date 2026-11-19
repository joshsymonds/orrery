import type { Epic, EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import type { WatchResponse } from "./gen/orrery/v1/orrery_pb";

// Affects is what an Events.Watch event can change of what the page has loaded: the rows loadRows reads, and the epics
// loadEpics reads.
export interface Affects {
  rows: boolean;
  epics: boolean;
}

const nothing: Affects = { rows: false, epics: false };
const rowsOnly: Affects = { rows: true, epics: false };
const epicsOnly: Affects = { rows: false, epics: true };
const both: Affects = { rows: true, epics: true };

// affectsOf is what event can change, given steps, the ids of the step sessions the loaded epics hold.
//
// The rows are the panes, the pane sessions and the headless sessions. A pane session changes only with a session
// event. A headless session's row reads its session columns, which only engine_added and engine_changed write, the first
// prompt, the newest main-conversation reply, which is a finished assistant text block outside any tool use, and the
// time of that reply or of the newest request that was not refused. So a block of another kind, a refused request, a
// withdrawn request, a turn's end, an interrupt and a background task change no row, and neither does an engine_text,
// which is the text so far of an unfinished block and is never stored as an event. A step session is never a row.
//
// The epics change with no event of their own, so an event can only hint that the epics' listed messages moved. A step
// session's start, state change, prompt, turn end, interrupt and requests are such hints, and so is the start of a
// session the page does not know, which can be a new step. A step's blocks, background tasks and streaming text are
// not: the poll's tick reads the epics either way.
//
// An event of a kind this page does not know can change either.
export function affectsOf(event: WatchResponse, steps: ReadonlySet<string>): Affects {
  const step = steps.has(event.sessionId);
  const payload = event.event;
  switch (payload.case) {
    case "engineText":
    case "engineTask":
      return nothing;
    case "sessionAdded":
    case "sessionChanged":
    case "sessionRemoved":
      return rowsOnly;
    case "engineAdded":
      return step ? epicsOnly : both;
    case "engineChanged":
    case "enginePrompt":
      return step ? epicsOnly : rowsOnly;
    case "engineRequest":
      return step ? epicsOnly : payload.value.refused ? nothing : rowsOnly;
    case "engineRequestCanceled":
    case "engineTurnEnded":
    case "engineInterrupt":
      return step ? epicsOnly : nothing;
    case "engineBlock": {
      const block = payload.value;
      const reply = block.kind === "text" && block.role === "assistant" && block.parentToolUseId === "";
      return !step && reply ? rowsOnly : nothing;
    }
    default:
      return both;
  }
}

// stepSessions is the ids of the step sessions the epics hold: each listed epic's running steps and each held detail's
// steps. A step's id is its session's.
export function stepSessions(epics: Epic[], details: ReadonlyMap<string, EpicsGetResponse>): Set<string> {
  const ids = new Set<string>();
  for (const epic of epics) {
    for (const step of epic.runningSteps) {
      ids.add(step.id);
    }
  }
  for (const detail of details.values()) {
    for (const step of detail.steps) {
      ids.add(step.id);
    }
  }
  return ids;
}
