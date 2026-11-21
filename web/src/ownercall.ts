import { useRef, useState } from "preact/hooks";

import { steerFailureOf } from "./steer";

// CallOutcome is how an owner call ends: landed, refused by orreryd with its reason, or unanswered, when the call may have
// gone through.
export type CallOutcome = { kind: "landed" } | { kind: "refused"; reason: string } | { kind: "unanswered" };

// sendOnce makes one owner call. It resolves landed when the call answers, and otherwise classifies its failure: a call
// that gave no answer is unanswered, and any other failure is orreryd's refusal, shown with its message. It is never
// retried.
export async function sendOnce(call: () => Promise<unknown>): Promise<CallOutcome> {
  try {
    await call();
    return { kind: "landed" };
  } catch (error) {
    const failure = steerFailureOf(error);
    if (failure.kind === "unanswered") {
      return { kind: "unanswered" };
    }
    return { kind: "refused", reason: failure.reason !== "" ? failure.reason : "no reason was given" };
  }
}

// CallPhase is where an owner call stands: at rest, asking which control, sending its call, reading the epic back after
// the call, or unread, when the read after the call failed.
export type CallPhase = "rest" | "asking" | "sending" | "reading" | "unread";

// OwnerCall is the call state a group of irreversible owner controls shares. ask opens a control's confirm from rest, keep
// closes it, confirm sends the call once, and readAgain reads the epic again from unread.
export interface OwnerCall {
  phase: CallPhase;
  asking: string;
  line: string;
  refused: boolean;
  disabled: boolean;
  ask(which: string): void;
  keep(): void;
  confirm(send: () => Promise<CallOutcome>): void;
  readAgain(): void;
}

interface View {
  phase: CallPhase;
  asking: string;
  line: string;
  refused: boolean;
}

// The lines an answered call shows: while the epic is read again, once the read answers, and once the read fails.
interface AnsweredLines {
  reading: string;
  rest: string;
  unread: string;
}

const answeredLines: Record<"landed" | "unanswered", AnsweredLines> = {
  landed: {
    reading: "Sent. Reading the epic again…",
    rest: "",
    unread: "Sent. The epic could not be read again.",
  },
  unanswered: {
    reading: "No answer came: it may have gone through. Reading the epic again…",
    rest: "No answer came: it may have gone through.",
    unread: "No answer came: it may have gone through. The epic could not be read again.",
  },
};

const sendingLine = "Sending…";

// atRest is the view at rest with the line it shows.
function atRest(line: string, refused: boolean): View {
  return { phase: "rest", asking: "", line, refused };
}

// useOwnerCall holds the call state for a group of owner controls. reread reads the epic again and resolves true when the
// read answers, and false when it fails. A landed or unanswered call reads the epic again once its answer is in, and
// stays disabled until that read answers.
export function useOwnerCall(reread: () => Promise<boolean>): OwnerCall {
  const [view, setView] = useState<View>(atRest("", false));
  // The view as last set, so a step made before the next render sees the phase the last step left.
  const latest = useRef<View>(view);
  // The answer of the last call that landed or went unanswered, which readAgain reads the epic again for.
  const answer = useRef<"landed" | "unanswered">("landed");

  const set = (next: View) => {
    latest.current = next;
    setView(next);
  };

  const read = (which: "landed" | "unanswered") => {
    answer.current = which;
    const lines = answeredLines[which];
    set({ phase: "reading", asking: latest.current.asking, line: lines.reading, refused: false });
    const unread: View = { phase: "unread", asking: latest.current.asking, line: lines.unread, refused: false };
    // A read that rejects is a read that failed.
    reread().then(
      (answered) => set(answered ? atRest(lines.rest, false) : unread),
      () => set(unread),
    );
  };

  const ask = (which: string) => {
    if (latest.current.phase !== "rest") {
      return;
    }
    set({ phase: "asking", asking: which, line: latest.current.line, refused: latest.current.refused });
  };

  const keep = () => {
    if (latest.current.phase !== "asking") {
      return;
    }
    set(atRest(latest.current.line, latest.current.refused));
  };

  const confirm = (send: () => Promise<CallOutcome>) => {
    if (latest.current.phase !== "asking") {
      return;
    }
    set({ phase: "sending", asking: latest.current.asking, line: sendingLine, refused: false });
    // A send that rejects gave no answer this hook can read, so it may have gone through.
    send().then(
      (outcome) => {
        if (outcome.kind === "refused") {
          set(atRest(`Refused: ${outcome.reason}`, true));
          return;
        }
        read(outcome.kind);
      },
      () => read("unanswered"),
    );
  };

  const readAgain = () => {
    if (latest.current.phase !== "unread") {
      return;
    }
    read(answer.current);
  };

  return {
    phase: view.phase,
    asking: view.asking,
    line: view.line,
    refused: view.refused,
    disabled: view.phase === "sending" || view.phase === "reading" || view.phase === "unread",
    ask,
    keep,
    confirm,
    readAgain,
  };
}
