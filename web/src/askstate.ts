import type { AskPhase } from "./ask";
import type { AskModel, AskOption } from "./askmodel";
import type { Row } from "./rows";

// The answer's life for the one Ask the middle conversation shows. idle holds no answer. sending is an answer in flight;
// sent is one the server took, which the Ask keeps showing as sending until the stream drops its question; error is one
// the server refused or failed; answered is one whose question the stream no longer shows. key names the question
// answered, n and name the option picked, and question is the question's text for the answered line.
export type AskState =
  | { phase: "idle" }
  | { phase: "sending" | "sent" | "error" | "answered"; key: string; n: number; name: string; question: string };

// The words an Ask shows when the server refuses an answer because the question changed before it arrived.
const refusedWords = "Not answered: the question changed before your answer arrived. This is what it asks now.";

// askKey names the question an Ask answers: a pane's row and its question hash, or a headless session's open request.
// It is null for an Ask with no answer target, which an epic's question is.
export function askKey(row: Row, model: AskModel): string | null {
  const target = model.target;
  if (target === null) {
    return null;
  }
  return target.kind === "pane" ? `${row.key}#${target.questionHash}` : target.requestId;
}

// press starts an answer to the question key names. A press while an answer is sending or sent, for any question, sends
// nothing and answers null.
export function press(state: AskState, key: string, option: AskOption, question: string): AskState | null {
  if (state.phase === "sending" || state.phase === "sent") {
    return null;
  }
  return { phase: "sending", key, n: option.n, name: option.name, question };
}

// settle records what the server said about the answer in flight: taken (sent) or refused or failed (error). With no
// answer in flight there is nothing to settle.
export function settle(state: AskState, ok: boolean): AskState {
  if (state.phase !== "sending") {
    return state;
  }
  return { ...state, phase: ok ? "sent" : "error" };
}

// observe moves an answer on with the key of the question the stream shows now, or null for none. A sent answer folds to
// answered once the stream no longer shows its question. An answered line stays until a different question shows. A
// refused answer clears once the question changes.
export function observe(state: AskState, currentKey: string | null): AskState {
  switch (state.phase) {
    case "idle":
    case "sending":
      return state;
    case "sent":
      return currentKey === state.key ? state : { ...state, phase: "answered" };
    case "answered":
      return currentKey === null || currentKey === state.key ? state : { phase: "idle" };
    case "error":
      return currentKey === state.key ? state : { phase: "idle" };
  }
}

// askView is the Ask's props for the question key names: its phase, the option picked, and the note. An answer in flight
// or taken shows sending with its option held; a refused one shows the refused-answer words. Any other question is open.
export function askView(state: AskState, key: string): { phase: AskPhase; picked: number | null; note: string } {
  if (state.phase === "idle" || state.phase === "answered" || state.key !== key) {
    return { phase: "open", picked: null, note: "" };
  }
  if (state.phase === "error") {
    return { phase: "error", picked: null, note: refusedWords };
  }
  return { phase: "sending", picked: state.n, note: "" };
}

// answeredOf is the answered line's words once an answer has folded, and null before that.
export function answeredOf(state: AskState): { n: number; name: string; question: string } | null {
  if (state.phase !== "answered") {
    return null;
  }
  return { n: state.n, name: state.name, question: state.question };
}
