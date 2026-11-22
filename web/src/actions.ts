import { Code, ConnectError } from "@connectrpc/connect";

import { conversationAsk, type AskTarget } from "./askmodel";
import type { createClients } from "./client";
import type { Conversation } from "./conversation";
import type { PickerRow } from "./gen/orrery/v1/orrery_pb";
import type { Row } from "./rows";

export type Clients = ReturnType<typeof createClients>;

// The state a pane row shows while its Claude Code waits on a question, as rows.ts names it.
const stateWaiting = "waiting";

// The digits that answer a question: 1 to 9, one key each.
const lastKeyedOption = 9;

// markSeen records that the user has seen the row: Panes.MarkSeen for a pane, Control.Seen for a session.
export async function markSeen(clients: Clients, row: Row): Promise<void> {
  if (row.kind === "pane") {
    await clients.panes.markSeen({ pane: row.id });
  } else {
    await clients.control.seen({ id: row.id });
  }
}

// answer sends the choice (a digit from 1 to 9) to the row's open question and resolves true. A pane answers by its
// question hash, so the keys land only while the pane still shows that question. A session answers the open request by
// its id, with the option at the choice's position. It resolves false, sending nothing, when the choice is not a digit
// from 1 to 9, when a pane's question offers no option of that number, or when the row has no answerable question.
export async function answer(
  clients: Clients,
  row: Row,
  conversation: Conversation | null,
  choice: number,
): Promise<boolean> {
  if (!Number.isInteger(choice) || choice < 1 || choice > lastKeyedOption) {
    return false;
  }
  if (row.kind === "pane") {
    // A pane's digit lands only on one of its question's own answerable options: a digit past the last sends nothing.
    const ask = conversationAsk(row, conversation);
    const offered = ask?.options.some((option) => option.n === choice && option.answerable) ?? false;
    if (row.state !== stateWaiting || row.questionHash === "" || !offered) {
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

// reasonOf runs call and resolves "" when the server took it, or the server's reason when it refused the call or the call
// failed.
async function reasonOf(call: () => Promise<unknown>): Promise<string> {
  try {
    await call();
    return "";
  } catch (error) {
    return ConnectError.from(error).rawMessage;
  }
}

// notNow takes the target's open question off the owner's list through Control.NotNow. A pane names its question by its
// id, question hash and changed time; a headless session by its id and request id.
export function notNow(clients: Clients, target: AskTarget): Promise<string> {
  return reasonOf(() =>
    clients.control.notNow(
      target.kind === "pane"
        ? { kind: "pane", id: target.id, questionHash: target.questionHash, changed: BigInt(target.changed) }
        : { kind: "headless", id: target.id, requestId: target.requestId },
    ),
  );
}

// bringBack puts the target's question back on the owner's list through Control.BringBack, named as notNow names it.
export function bringBack(clients: Clients, target: AskTarget): Promise<string> {
  return reasonOf(() =>
    clients.control.bringBack(
      target.kind === "pane"
        ? { kind: "pane", id: target.id, questionHash: target.questionHash, changed: BigInt(target.changed) }
        : { kind: "headless", id: target.id, requestId: target.requestId },
    ),
  );
}

// decline denies the target's open question and ends its turn through Control.Decline, named as notNow names it but
// without the changed time.
export function decline(clients: Clients, target: AskTarget): Promise<string> {
  return reasonOf(() =>
    clients.control.decline(
      target.kind === "pane"
        ? { kind: "pane", id: target.id, questionHash: target.questionHash }
        : { kind: "headless", id: target.id, requestId: target.requestId },
    ),
  );
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

// withdraw takes back a pane's queued input and resolves its text as one message, or none when the pull is empty or
// the pane has nothing queued: PullQueued's FailedPrecondition, which is most Escapes on a working pane. A session
// resolves none and makes no call: its queued messages are taken back by interrupt, when Escape interrupts it.
export async function withdraw(clients: Clients, row: Row): Promise<string[]> {
  if (row.kind === "session") {
    return [];
  }
  let text: string;
  try {
    text = await pullQueued(clients, row);
  } catch (error) {
    if (ConnectError.from(error).code === Code.FailedPrecondition) {
      return [];
    }
    throw error;
  }
  return text === "" ? [] : [text];
}

// withdrawQueued takes back one of a session's queued messages by its prompt id, through Control.Withdraw, and resolves
// the text it took back, or none when Claude already took the message in.
export async function withdrawQueued(clients: Clients, row: Row, promptId: string): Promise<string[]> {
  const reply = await clients.control.withdraw({ id: row.id, promptId });
  return reply.texts;
}

// interrupt interrupts the row's running turn. For a session it is Control.Interrupt, and it resolves the texts of the
// queued messages Claude cancelled at the interrupt, in send order. For a pane it sends the Escape key and resolves
// none.
export async function interrupt(clients: Clients, row: Row): Promise<string[]> {
  if (row.kind === "pane") {
    await clients.panes.sendKeys({ pane: row.id, keys: ["Escape"] });
    return [];
  }
  const reply = await clients.control.interrupt({ id: row.id });
  return reply.texts;
}

// ask sends the words to Picker.Ask, exactly as typed, and resolves the rows that match them. The call waits 90 seconds
// at most, then fails with DeadlineExceeded.
export async function ask(clients: Clients, words: string): Promise<PickerRow[]> {
  const reply = await clients.picker.ask({ text: words }, { timeoutMs: 90_000 });
  return reply.rows;
}

// run sends a row's token to Picker.Run and resolves the pane the run answered.
export async function run(clients: Clients, token: string): Promise<string> {
  const reply = await clients.picker.run({ token });
  return reply.pane;
}

// newPromptId returns a fresh random id for a Say, as the phone's UUID.randomUUID() does.
export function newPromptId(): string {
  return crypto.randomUUID();
}

// newStartId returns a fresh start id: 32 lowercase hex digits, the UUID with its hyphens removed.
export function newStartId(): string {
  return crypto.randomUUID().replace(/-/g, "");
}

// start sends the folder, the message and the start id to Control.Start, with no model, and resolves the session id the
// reply carries.
export async function start(clients: Clients, dir: string, message: string, id: string): Promise<string> {
  const reply = await clients.control.start({ dir, prompt: message, id });
  return reply.id;
}
