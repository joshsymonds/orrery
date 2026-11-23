import { Code, ConnectError } from "@connectrpc/connect";

import type { Clients } from "./actions";
import type { EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import type { Row } from "./rows";

// The states a headless session holds once it has stopped or failed, which ends the conversation it steers.
const stateStopped = "stopped";
const stateFailed = "failed";

// The codes a call's failure reads as the server not answering, so the move may have gone through.
const noAnswerCodes = new Set([Code.Unavailable, Code.DeadlineExceeded, Code.Unknown]);

// inProject is whether a conversation's directory belongs to the epic's project: the repository itself, a directory under
// it, or a worktree under <repository>.worktrees/ or <repository>-worktrees/. An empty repository holds no project.
export function inProject(dir: string, repository: string): boolean {
  if (repository === "") {
    return false;
  }
  return (
    dir === repository ||
    dir.startsWith(`${repository}/`) ||
    dir.startsWith(`${repository}.worktrees/`) ||
    dir.startsWith(`${repository}-worktrees/`)
  );
}

// steererRowOf is the row of the conversation that steers an epic, the row whose archive id is that session. A row with
// no archive kind does not carry one. An epic no conversation steers, or whose steerer the rows do not hold, has no row.
export function steererRowOf(rows: readonly Row[], steering: string): Row | null {
  if (steering === "") {
    return null;
  }
  return rows.find((row) => row.archiveKind !== "none" && row.archiveId === steering) ?? null;
}

// isLiveConversation is whether a row is a conversation that can take an epic: not archived, a headless session that has
// not stopped or failed, or a pane with an orrery id whose state is not empty.
export function isLiveConversation(row: Row): boolean {
  if (row.archived) {
    return false;
  }
  if (row.archiveKind === "headless") {
    return row.state !== stateStopped && row.state !== stateFailed;
  }
  if (row.kind === "pane" && row.archiveKind === "session") {
    return row.state !== "";
  }
  return false;
}

export interface SteerChoices {
  current: Row | null;
  others: Row[];
}

// steerChoices splits the rows for the steering menu: the epic's current steerer, and the other live conversations in
// the epic's project, in the order the rows hold them.
export function steerChoices(rows: readonly Row[], steering: string, repository: string): SteerChoices {
  return {
    current: steererRowOf(rows, steering),
    others: rows.filter(
      (row) =>
        isLiveConversation(row) && inProject(row.project, repository) && row.archiveId !== steering,
    ),
  };
}

// conversationEnded is whether the conversation that steers an epic has ended: no listed row steers it, or its steerer is a
// headless session that has stopped or failed. An epic with no steering session at all is not ended.
export function conversationEnded(rows: readonly Row[], steering: string): boolean {
  if (steering === "") {
    return false;
  }
  const row = steererRowOf(rows, steering);
  return (
    row === null ||
    (row.archiveKind === "headless" && (row.state === stateStopped || row.state === stateFailed))
  );
}

// steersLive is whether an epic is live, which a conversation can steer by a move.
export function steersLive(detail: EpicsGetResponse | undefined): boolean {
  return detail?.epic?.live === true;
}

export type SteerFailure = { kind: "refused"; reason: string } | { kind: "unanswered" };

// steerFailureOf reads a failed move or continue: a call that never reached the server or failed unavailable, past its
// deadline or unknown did not get an answer, and any other failure is orreryd's refusal, shown with its message.
export function steerFailureOf(error: unknown): SteerFailure {
  const failure = ConnectError.from(error);
  if (noAnswerCodes.has(failure.code)) {
    return { kind: "unanswered" };
  }
  return { kind: "refused", reason: failure.rawMessage };
}

export interface SteerCalls {
  move: (epic: string, session: string) => Promise<void>;
  continue: (epic: string) => Promise<string>; // the started session's id, which may be ""
  get: (epic: string) => Promise<EpicsGetResponse>;
}

// steerCallsOf makes the steering calls over the clients. Each call is sent once, with the ids it was given.
export function steerCallsOf(clients: Clients): SteerCalls {
  return {
    move: async (epic, session) => {
      await clients.epics.move({ id: epic, session });
    },
    continue: async (epic) => (await clients.epics.continue({ id: epic })).session,
    get: (epic) => clients.epics.get({ id: epic }),
  };
}
