import type { createClients } from "./client";
import type { Conversation } from "./conversation";
import type { Row } from "./rows";

export type Clients = ReturnType<typeof createClients>;

// The state a pane row shows while its Claude Code waits on a question, as rows.ts names it.
const stateWaiting = "waiting";

// markSeen records that the user has seen the row: Panes.MarkSeen for a pane, Control.Seen for a session.
export async function markSeen(clients: Clients, row: Row): Promise<void> {
  if (row.kind === "pane") {
    await clients.panes.markSeen({ pane: row.id });
  } else {
    await clients.control.seen({ id: row.id });
  }
}

// answer sends the choice (1-3) to the row's open question and resolves true. A pane answers by its question hash, so
// the keys land only while the pane still shows that question. A session answers the open request by its id, with the
// option at the choice's position. It resolves false, sending nothing, when the row has no answerable question.
export async function answer(
  clients: Clients,
  row: Row,
  conversation: Conversation | null,
  choice: 1 | 2 | 3,
): Promise<boolean> {
  if (row.kind === "pane") {
    if (row.state !== stateWaiting || row.questionHash === "") {
      return false;
    }
    await clients.panes.sendKeys({ pane: row.id, keys: [String(choice)], expectQuestion: row.questionHash });
    return true;
  }
  const request = conversation?.state?.request;
  const option: string | undefined = request?.options[choice - 1];
  if (request === undefined || option === undefined) {
    return false;
  }
  await clients.control.answer({ id: row.id, requestId: request.id, answer: option });
  return true;
}

// send sends text to the row's Claude Code: SendText for a pane, Say for a session with the given prompt id.
export async function send(clients: Clients, row: Row, text: string, promptId: string): Promise<void> {
  if (row.kind === "pane") {
    await clients.panes.sendText({ pane: row.id, text });
  } else {
    await clients.control.say({ id: row.id, text, promptId });
  }
}

// pullQueued takes back a pane's queued messages and resolves their text. A session stream carries no queue, so a
// session resolves "" and sends nothing.
export async function pullQueued(clients: Clients, row: Row): Promise<string> {
  if (row.kind === "session") {
    return "";
  }
  const reply = await clients.panes.pullQueued({ pane: row.id });
  return reply.text;
}

// newPromptId returns a fresh random id for a Say, as the phone's UUID.randomUUID() does.
export function newPromptId(): string {
  return crypto.randomUUID();
}
