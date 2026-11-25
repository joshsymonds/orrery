// Who a stage record names, in round 8's words: the three makers of a design's log line, and the three makers of a
// stage call that a question or a record quotes. The makers' kinds are the store's: owner, conversation and orreryd for
// a log line, and owner_serve, owner_conversation and conversation for a stage call.

import type { RecordMaker } from "./gen/orrery/v1/designs_pb";

// whoOfLog names who did a design log line's thing from the line's maker kind: you, the Director, or orreryd. A kind
// that is none of these is named as it is.
export function whoOfLog(kind: string): string {
  switch (kind) {
    case "owner":
      return "you";
    case "conversation":
      return "the Director";
    case "orreryd":
      return "orreryd";
    default:
      return kind;
  }
}

// whoOfMaker names who made a stage call and the evidence it rests on: you in Orrery; you in the conversation, with
// the words you quoted there; or the Director, with its reason and the delegation of yours it rests on. An open
// question has no maker, so undefined names no one.
export function whoOfMaker(maker: RecordMaker | undefined): string {
  if (maker === undefined) {
    return "";
  }
  switch (maker.kind) {
    case "owner_serve":
      return "you, in Orrery";
    case "owner_conversation":
      return maker.quote === "" ? "you, in the conversation" : `you, in the conversation, quoting “${maker.quote}”`;
    case "conversation": {
      const because = `the Director, because ${maker.reason}`;
      return maker.delegation === "" ? because : `${because}, under your words “${maker.delegation}”`;
    }
    default:
      return maker.kind;
  }
}
