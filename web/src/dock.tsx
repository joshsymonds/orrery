import type { JSX, Ref } from "preact";
import { useLayoutEffect, useRef, useState } from "preact/hooks";

import { Ctl } from "./ctl";
import { type DockFacts, dockLines, placeholder, stateLine } from "./dockwords";
import { useFlash } from "./flash";
import type { ConversationItem, QueuedMessage, WaitingMessage } from "./gen/orrery/v1/orrery_pb";
import { type Sending, type SentText, promptHeld, sameText, withoutTexts } from "./sending";
import { outcomeOf } from "./stepbox";
import { ReplyText } from "./text";

const sendFailedLine = "Not sent. Your draft is kept; press Enter to try again.";
const pullFailedLine = "Could not pull the message back.";
const takesNextLine = "Claude takes it at its next tool call.";
const alreadyTookLine = "Claude already took it in.";
const withdrawFailedLine = "Could not withdraw the message.";

// deskQuery holds at a desk width. Narrower than it, the dock says its short queue prompt.
const deskQuery = "(min-width: 760px)";

interface FailedSend {
  text: string;
  promptId: string;
}

// A message this dock sent while its conversation was working, with the newest item the conversation held as it was
// sent. It stays above the box until the conversation's items hold its prompt, it leaves the session queue after it was
// in it (wasQueued), the turn ends or its send fails.
interface LocalQueued extends Sending {
  wasQueued: boolean;
}

// settleLocal drops each local entry whose prompt items hold, and each one that has left the session queue after it was
// in it, and marks the ones the queue carries as queued. It returns entries itself when nothing changes.
function settleLocal(entries: LocalQueued[], items: ConversationItem[], queue: QueuedMessage[]): LocalQueued[] {
  let changed = false;
  const kept: LocalQueued[] = [];
  for (const entry of entries) {
    const inQueue = queue.some((message) => message.promptId === entry.promptId);
    if (promptHeld(entry, items) || (entry.wasQueued && !inQueue)) {
      changed = true;
    } else if (inQueue && !entry.wasQueued) {
      changed = true;
      kept.push({ ...entry, wasQueued: true });
    } else {
      kept.push(entry);
    }
  }
  return changed ? kept : entries;
}

// QueueRow is one line of the queue above the box that carries only its text: a waiting message or a message this
// dock sent.
interface QueueRow {
  key: string;
  text: string;
  canceled: boolean;
}

// WithdrawOutcome is what a withdraw of a session's queued message came to, when it did not take the text back: took when
// Claude had already taken the message in, failed when the call was refused.
type WithdrawOutcome = "took" | "failed";

// DockHandle is what the page calls on a dock when its row's Escape takes queued messages back. append puts their texts
// in the box after its text, joined by blank lines. forget drops the local entry each text matches, since the session
// queue no longer carries those messages.
export interface DockHandle {
  append: (texts: string[]) => void;
  forget: (texts: string[]) => void;
}

// Dock is the box under the reader and its state line. The line and the box's placeholder read the row's facts, and the
// box carries the line's tone as data-state. Enter and the send button send the text once; Shift+Enter or Alt+Enter puts
// a line break at the cursor instead. A send reports itself to onSending as it starts, and to onUnsent if it rejects. A
// failed send keeps the draft and its prompt id and is never resent by the dock. Up in an empty box pulls the newest
// queued text back. In an empty box with no modifier held, Page Up and Page Down go to onPage, and / goes to onFind. The
// queue draws above the box under one head that counts its queued rows, oldest first, each message as its text: the
// conversation's waiting messages, then the session's queued messages, then the messages this dock sent while the
// conversation was working, except those the session queue carries under their prompt id. Such a message leaves once the
// conversation's items hold its prompt, or once it leaves the session queue it was in, so a message that became a prompt
// is not drawn as queued while the conversation stays working. A canceled message is tagged. A session's queued message
// carries a withdraw, which takes it back into the box, and a note on what Claude did. A session's discarded message
// draws after the rows as not delivered, with a control that puts it back in the box. The state line's Interrupt calls
// onInterrupt, and its Resume calls resume; a refused Resume shows its line under the box.
export function Dock(props: {
  facts: DockFacts; // the row's facts, which the state line and the placeholder read
  now: number; // unix seconds, for the state line's ages and clocks
  send: (text: string, promptId: string) => Promise<void>;
  pull: (() => Promise<string>) | null; // null: no pull for this row (sessions)
  newPromptId: () => string;
  inputRef?: Ref<HTMLTextAreaElement>; // the page focuses it on `i`
  register?: (handle: DockHandle | null) => void; // gets the handle while mounted, and null once unmounted
  queued?: WaitingMessage[]; // the row's waiting and canceled messages, oldest first; none drawn when absent
  sessionQueue?: QueuedMessage[]; // the session's queued messages, oldest first; none drawn when absent
  withdraw?: (promptId: string) => Promise<string[]>; // takes a session's queued message back, resolving its texts; no withdraw control when absent
  items?: ConversationItem[]; // the conversation's items, whose prompts end the queued messages sent here
  onSending?: (entry: SentText) => void; // called as a send starts, before it is awaited
  onUnsent?: (promptId: string) => void; // called when a send rejects
  onPage?: (direction: "up" | "down") => boolean; // an empty box's Page Up or Page Down; true prevents the key
  onFind?: () => void; // an empty box's /; the key is always prevented, so no slash is typed
  onInterrupt?: () => void; // the state line's Interrupt: the row's Escape path; no Interrupt control when absent
  resume?: () => Promise<void>; // the state line's Resume on a cut-off session; no Resume control when absent
}): JSX.Element {
  const {
    facts,
    now,
    send,
    pull,
    newPromptId,
    inputRef,
    register,
    queued = [],
    sessionQueue = [],
    withdraw,
    items = [],
    onSending,
    onUnsent,
    onPage,
    onFind,
    onInterrupt,
    resume,
  } = props;
  const { state } = facts;
  const line = stateLine(facts, now);
  const narrow = !window.matchMedia(deskQuery).matches;
  const [text, setText] = useState("");
  const [error, setError] = useState("");
  const [local, setLocal] = useState<LocalQueued[]>([]);
  // The outcome of each session message a withdraw did not take back, by prompt id.
  const [outcomes, setOutcomes] = useState<Map<string, WithdrawOutcome>>(() => new Map());
  // The prompt ids of the discarded messages put back in the box, whose rows are hidden for the dock's life.
  const [putBackIds, setPutBackIds] = useState<string[]>([]);
  const inFlight = useRef(false);
  const failed = useRef<FailedSend | null>(null);
  const [flashing, flash] = useFlash();
  // caret is the cursor a line break left for the render to park: setting the value moves the cursor to the end.
  const caret = useRef<{ box: HTMLTextAreaElement; at: number } | null>(null);

  // append puts texts in the box after its text, joined by blank lines.
  const append = (texts: string[]) => {
    const returned = texts.join("\n\n");
    setText((current) => (current === "" ? returned : `${current}\n\n${returned}`));
  };

  // The page holds the handle while this dock is mounted, and drops it when the dock unmounts.
  useLayoutEffect(() => {
    register?.({
      append,
      forget: (texts) => {
        setLocal((current) => withoutTexts(current, texts));
      },
    });
    return () => register?.(null);
  }, [register]);

  useLayoutEffect(() => {
    const pending = caret.current;
    if (pending !== null) {
      caret.current = null;
      pending.box.setSelectionRange(pending.at, pending.at);
    }
  }, [text]);

  // A turn that ends takes the messages sent during it off the queue.
  useLayoutEffect(() => {
    if (state !== "working") {
      setLocal((current) => (current.length === 0 ? current : []));
    }
  }, [state]);

  // A message whose prompt the conversation holds, or that left the session queue, is no longer queued.
  useLayoutEffect(() => {
    setLocal((current) => settleLocal(current, items, sessionQueue));
  }, [items, sessionQueue]);

  async function submit(): Promise<void> {
    if (inFlight.current || text.trim() === "") {
      return;
    }
    flash();
    const sent = text;
    const pending = failed.current;
    const promptId = pending !== null && pending.text === sent ? pending.promptId : newPromptId();
    inFlight.current = true;
    setError("");
    onSending?.({ text: sent, promptId });
    if (state === "working") {
      setLocal((current) => [...current, { promptId, text: sent, after: items.at(-1), wasQueued: false }]);
    }
    try {
      await send(sent, promptId);
      failed.current = null;
      // Text typed while the send was in flight stays in the box.
      setText((current) => (current === sent ? "" : current));
    } catch {
      failed.current = { text: sent, promptId };
      setError(sendFailedLine);
      onUnsent?.(promptId);
      setLocal((current) => current.filter((entry) => entry.promptId !== promptId));
    } finally {
      inFlight.current = false;
    }
  }

  async function pullBack(): Promise<void> {
    if (pull === null) {
      return;
    }
    flash();
    try {
      const pulled = await pull();
      // Text typed while the pull was in flight follows the pulled text.
      setText((current) => (current === "" ? pulled : `${pulled}\n${current}`));
      setError("");
    } catch {
      setError(pullFailedLine);
    }
  }

  // withdrawRow takes a session's queued message back through take. Its texts go to the box; none means Claude already
  // took the message in, and a refused call is shown as its own line on the row. The row stays until the stream drops it.
  async function withdrawRow(take: (promptId: string) => Promise<string[]>, promptId: string): Promise<void> {
    try {
      const texts = await take(promptId);
      if (texts.length > 0) {
        append(texts);
      }
      setOutcome(promptId, texts.length === 0 ? "took" : null);
    } catch {
      setOutcome(promptId, "failed");
    }
  }

  // setOutcome records the outcome of a withdraw of one message, or clears it when null.
  function setOutcome(promptId: string, outcome: WithdrawOutcome | null): void {
    setOutcomes((current) => {
      const next = new Map(current);
      if (outcome === null) {
        next.delete(promptId);
      } else {
        next.set(promptId, outcome);
      }
      return next;
    });
  }

  // resumeRow runs the state line's Resume. A refusal shows its message under the box; any other failure shows the
  // unknown line, since the session may have resumed.
  async function resumeRow(run: () => Promise<void>): Promise<void> {
    try {
      await run();
      setError("");
    } catch (failure) {
      const outcome = outcomeOf(failure);
      setError(outcome.outcome === "refused" ? dockLines.resumeRefused(outcome.message) : dockLines.resumeUnknown);
    }
  }

  // putBackRow puts a discarded message's text in the box and hides its row.
  function putBackRow(messageText: string, promptId: string): void {
    append([messageText]);
    setPutBackIds((current) => [...current, promptId]);
  }

  // lineBreakAt replaces the box's selection with a line break and leaves the cursor just after it.
  function lineBreakAt(box: HTMLTextAreaElement): void {
    const { value, selectionStart, selectionEnd } = box;
    caret.current = { box, at: selectionStart + 1 };
    setText(`${value.slice(0, selectionStart)}\n${value.slice(selectionEnd)}`);
  }

  function onKeyDown(event: JSX.TargetedKeyboardEvent<HTMLTextAreaElement>): void {
    if (event.key === "Enter") {
      event.preventDefault();
      if (event.shiftKey || event.altKey) {
        lineBreakAt(event.currentTarget);
      } else {
        void submit();
      }
    } else if (event.key === "ArrowUp" && text === "" && pull !== null) {
      event.preventDefault();
      void pullBack();
    } else if (text === "" && !event.shiftKey && !event.altKey && !event.ctrlKey && !event.metaKey) {
      if ((event.key === "PageUp" || event.key === "PageDown") && onPage !== undefined) {
        if (onPage(event.key === "PageUp" ? "up" : "down")) {
          event.preventDefault();
        }
      } else if (event.key === "/" && onFind !== undefined) {
        event.preventDefault();
        onFind();
      }
    }
  }

  // The local messages show only while the conversation is working. A message the conversation's session queue carries
  // under its prompt id is drawn from that queue, in whatever state it is, and not from here.
  const sentHere = state === "working" ? local : [];
  const paneRows: QueueRow[] = queued.map((message, index) => ({
    key: `queued-${index}`,
    text: message.text,
    canceled: message.canceled,
  }));
  const localRows: QueueRow[] = sentHere
    .filter((entry) => !sessionQueue.some((message) => message.promptId === entry.promptId))
    .filter((entry) => !queued.some((message) => !message.canceled && sameText(message.text, entry.text)))
    .map((entry) => ({ key: `local-${entry.promptId}`, text: entry.text, canceled: false }));
  const sessionRows = sessionQueue.filter((message) => message.state === "queued");
  const undelivered = sessionQueue.filter(
    (message) => message.state === "discarded" && !putBackIds.includes(message.promptId),
  );
  const queuedCount = paneRows.length + sessionRows.length + localRows.length;
  const plainRow = (row: QueueRow) => (
    <li key={row.key} class={row.canceled ? "queued canceled" : "queued"}>
      {row.canceled && <span class="tag">canceled</span>}
      <span class="text">
        <ReplyText text={row.text} />
      </span>
    </li>
  );

  return (
    <div class="dock">
      {(queuedCount > 0 || undelivered.length > 0) && (
        <ol class="queue">
          {queuedCount > 0 && <li class="queue-head">{`${queuedCount} queued`}</li>}
          {paneRows.map(plainRow)}
          {sessionRows.map((message) => {
            const outcome = outcomes.get(message.promptId);
            return (
              <li key={`session-${message.promptId}`} class="queued">
                <span class="text">
                  <ReplyText text={message.text} />
                </span>
                <span class="note">
                  {outcome === "took" ? alreadyTookLine : outcome === "failed" ? withdrawFailedLine : takesNextLine}
                </span>
                {withdraw !== undefined && outcome !== "took" && (
                  <button
                    type="button"
                    class="withdraw"
                    aria-label="withdraw"
                    onClick={() => void withdrawRow(withdraw, message.promptId)}
                  >
                    Withdraw
                  </button>
                )}
              </li>
            );
          })}
          {localRows.map(plainRow)}
          {undelivered.map((message) => (
            <li key={`undelivered-${message.promptId}`} class="undelivered">
              <span class="tag">not delivered</span>
              <span class="text">
                <ReplyText text={message.text} />
              </span>
              <button
                type="button"
                class="put-back"
                aria-label="put back"
                onClick={() => putBackRow(message.text, message.promptId)}
              >
                Put back in the box
              </button>
            </li>
          ))}
        </ol>
      )}
      <div class={`dock-box${flashing ? " flash" : ""}`} data-state={line.tone}>
        <div class="statebar">
          <span class="word">{line.word}</span>
          {(line.tool !== "" || line.what !== "") && (
            <span class="what">
              {line.tool !== "" && <span class="name">{line.tool}</span>}
              {line.tool !== "" && line.what !== "" && " "}
              {line.what}
            </span>
          )}
          {line.clock !== "" && <span class="clock">{line.clock}</span>}
          <span class="end">
            {line.control === "interrupt" && onInterrupt !== undefined && (
              <Ctl label="Interrupt" icon="stop" tone="bad" kbd="esc" onClick={onInterrupt} />
            )}
            {line.control === "interrupting" && <Ctl label="Interrupting…" disabled />}
            {line.control === "resume" && resume !== undefined && (
              <Ctl label="Resume" icon="play" tone="go" onClick={() => void resumeRow(resume)} />
            )}
          </span>
        </div>
        <div class="dock-input">
          <textarea
            ref={inputRef}
            placeholder={placeholder(facts, narrow)}
            rows={1}
            value={text}
            onInput={(event) => setText(event.currentTarget.value)}
            onKeyDown={onKeyDown}
          />
          <button type="button" class="go" aria-label="send" onClick={() => void submit()}>
            ↑
          </button>
        </div>
      </div>
      {error !== "" && (
        <p class="inote error" role="alert">
          {error}
        </p>
      )}
    </div>
  );
}
