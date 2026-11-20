import type { JSX, Ref } from "preact";
import { useLayoutEffect, useRef, useState } from "preact/hooks";

import { Ctl } from "./ctl";
import {
  type DockFacts,
  type TrayRowFacts,
  dockLines,
  placeholder,
  stateLine,
  trayLabel,
  trayWhy,
  waitsLine,
} from "./dockwords";
import { useFlash } from "./flash";
import type { ConversationItem, PendingEvent, QueuedMessage, WaitingMessage } from "./gen/orrery/v1/orrery_pb";
import { outcomeOf } from "./refusal";
import { sendNow } from "./sendnow";
import { type Sending, type SentText, promptHeld, sameText, withoutTexts } from "./sending";
import { ReplyText } from "./text";

// deskQuery holds at a desk width. Narrower than it, the dock says its short queue prompt.
const deskQuery = "(min-width: 760px)";

// noEvents is what a dock holds when it is given no pending events. One array serves every render, so the waits line
// stays cleared after a send until the page passes a new list.
const noEvents: PendingEvent[] = [];

interface FailedSend {
  text: string;
  promptId: string;
}

// A message this dock sent while its conversation was working, with the newest item the conversation held as it was
// sent. It stays in the tray until the conversation's items hold its prompt, it leaves the session queue after it was
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

// WithdrawOutcome is what a withdraw of a session's queued message came to, when it did not take the text back: took when
// Claude had already taken the message in, failed when the call was refused.
type WithdrawOutcome = "took" | "failed";

// pendingKey names a set of pending events by each one's id and wait, so the same events carried by a later state read
// as unchanged.
function pendingKey(pending: readonly PendingEvent[]): string {
  return pending.map((event) => `${event.id}:${event.waits}`).join("|");
}

// lineIcons are the drawings of the icons the dock's lines lead with, from the round-6 wireframe's icons.js: the bell of
// the waits line, the chat of the about line and the warning of an error note.
const lineIcons = {
  bell: "M4 11V7.5a4 4 0 0 1 8 0V11l1 1.5H3zM6.5 13.5a1.5 1.5 0 0 0 3 0",
  chat: "M3 3h10v7H8l-3 3v-3H3z",
  warn: "M8 2.5l6 10.5H2zM8 6.5v3M8 11.2v.3",
} as const;

// LineIcon draws one of the dock's line icons, stroked in the line's colour and hidden from assistive technology, since
// the line's words say the same.
function LineIcon({ name }: { name: keyof typeof lineIcons }): JSX.Element {
  return (
    <svg class="icon" data-icon={name} viewBox="0 0 16 16" aria-hidden="true">
      <path d={lineIcons[name]} />
    </svg>
  );
}

// DockHandle is what the page calls on a dock when its row's Escape takes queued messages back. append puts their texts
// in the box after its text, joined by blank lines, and notes that they are back in the box. forget drops the local
// entry each text matches, since the session queue no longer carries those messages.
export interface DockHandle {
  append: (texts: string[]) => void;
  forget: (texts: string[]) => void;
}

// Dock is the box under the reader and its state line. The line and the box's placeholder read the row's facts, and the
// box carries the line's tone as data-state. Enter and the send button send the text once; Shift+Enter or Alt+Enter puts
// a line break at the cursor instead. A refused send shows its line under the box and keeps the draft; a send whose
// outcome is unknown keeps the draft and shows its note. Up in an empty box pulls the newest queued text back. In an
// empty box with no modifier held, Page Up and Page Down go to onPage, and / goes to onFind.
//
// With stepCalls the dock is a step's box: its send nudges the step, its Interrupt holds it, and More opens the
// question Stop {label}? before Stop calls the step's stop. A nudge keeps its draft when it fails, and its outcome
// shows on the line under the box as the step's own words.
//
// The tray draws under the state line, under one head that counts its rows, oldest first: the conversation's waiting
// messages, then the session's queued messages, then the messages this dock sent while the conversation was working,
// except those the session queue carries under their prompt id, then the session's discarded messages. A row's words
// say what Claude does with it. A session's queued message carries a take back, which puts its text in the box, and a
// note when Claude had already taken it in or the take back was refused. A discarded message is marked not delivered
// and carries a put back. The head says what the rows will do, and carries Take all back for a pane and Send now for a
// working session that has an interrupt: Send now interrupts the turn and says each cancelled text again, and the texts
// it could not say go back into the box. The waits line counts the pending events that wait on the owner, and the about
// line says what the message is about until Not about this clears it.
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
  withdraw?: (promptId: string) => Promise<string[]>; // takes a session's queued message back, resolving its texts; no take back control when absent
  items?: ConversationItem[]; // the conversation's items, whose prompts end the queued messages sent here
  onSending?: (entry: SentText) => void; // called as a send starts, before it is awaited
  onUnsent?: (promptId: string) => void; // called when a send rejects
  onPage?: (direction: "up" | "down") => boolean; // an empty box's Page Up or Page Down; true prevents the key
  onFind?: () => void; // an empty box's /; the key is always prevented, so no slash is typed
  onInterrupt?: () => void; // the state line's Interrupt: the row's Escape path; no Interrupt control when absent
  interrupt?: () => Promise<string[]>; // Send now's interrupt, resolving the texts it cancelled; no Send now when absent
  resume?: () => Promise<void>; // the state line's Resume on a cut-off session; no Resume control when absent
  pending?: PendingEvent[]; // the session's pending events, for the waits line
  about?: string; // what the message is about; no about line when absent
  aboutId?: string; // the id of what the message is about, such as F2, drawn as its chip before the words
  onClearAbout?: () => void; // Not about this: the about line is cleared, and this is called
  stepCalls?: { hold: () => Promise<void>; stop: () => Promise<void> }; // a step's hold and stop; its send nudges it
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
    interrupt,
    resume,
    pending = noEvents,
    about,
    aboutId,
    onClearAbout,
    stepCalls,
  } = props;
  const { state } = facts;
  const line = stateLine(facts, now);
  const narrow = !window.matchMedia(deskQuery).matches;
  const [text, setText] = useState("");
  const [error, setError] = useState("");
  const [note, setNote] = useState("");
  const [local, setLocal] = useState<LocalQueued[]>([]);
  // The outcome of each session message a take back did not take back, by prompt id.
  const [outcomes, setOutcomes] = useState<Map<string, WithdrawOutcome>>(() => new Map());
  // The prompt ids of the discarded messages put back in the box, whose rows are hidden for the dock's life.
  const [putBackIds, setPutBackIds] = useState<string[]>([]);
  // The pending events, by id and wait, that the last successful send left the waits line hidden for. It is drawn again
  // once the pending events differ from them, and not when a later state carries the same ones again.
  const [waitsCleared, setWaitsCleared] = useState<string | null>(null);
  // Whether a Send now is in flight: its control is spent until it settles, so a second press interrupts nothing.
  const [sendingNow, setSendingNow] = useState(false);
  const sendNowInFlight = useRef(false);
  // The about text Not about this cleared. The about line is drawn again only for a different about.
  const [aboutCleared, setAboutCleared] = useState<string | null>(null);
  const [focused, setFocused] = useState(false);
  // Whether a step's Stop question is open: its More pressed, and not yet confirmed or kept.
  const [stopOpen, setStopOpen] = useState(false);
  // The text of a step's nudge whose outcome is not known. Enter does not send it again until the text changes.
  const unknownText = useRef<string | null>(null);
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

  // The page holds the handle while this dock is mounted, and drops it when the dock unmounts. Its append also notes
  // that the texts are back in the box.
  useLayoutEffect(() => {
    register?.({
      append: (texts) => {
        append(texts);
        setNote(dockLines.backInBox(texts.length));
      },
      forget: (texts) => {
        setLocal((current) => withoutTexts(current, texts));
      },
    });
    return () => register?.(null);
  }, [register]);

  useLayoutEffect(() => {
    const pendingCaret = caret.current;
    if (pendingCaret !== null) {
      caret.current = null;
      pendingCaret.box.setSelectionRange(pendingCaret.at, pendingCaret.at);
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

  // unsentLine shows what a failed send came to: a refusal in the error line, and any other failure as the unknown note,
  // since the send may have landed.
  function unsentLine(failure: unknown): void {
    if (outcomeOf(failure).outcome === "refused") {
      setError(dockLines.sendRefused);
    } else {
      setNote(dockLines.sendUnknown);
    }
  }

  // clearLines drops the outcome lines a new call draws over.
  function clearLines(): void {
    setError("");
    setNote("");
  }

  // stepFailed shows a step call's failure: a refusal with its message, and any other failure as its unknown line.
  function stepFailed(failure: unknown, refused: string, unknownLine: string): void {
    const outcome = outcomeOf(failure);
    setError(outcome.outcome === "refused" ? `${refused}: ${outcome.message}` : unknownLine);
  }

  // nudgeStep sends a step's text as a nudge, once. A refused nudge keeps the draft. A nudge whose outcome is unknown
  // keeps it too, and Enter sends that text no more until it changes.
  async function nudgeStep(): Promise<void> {
    if (inFlight.current || text.trim() === "" || text === unknownText.current) {
      return;
    }
    const sent = text;
    inFlight.current = true;
    clearLines();
    try {
      await send(sent, newPromptId());
      unknownText.current = null;
      // Text typed while the nudge was in flight stays in the box.
      setText((current) => (current === sent ? "" : current));
      setNote("nudge landed");
    } catch (failure) {
      unknownText.current = outcomeOf(failure).outcome === "unknown" ? sent : null;
      stepFailed(failure, "nudge refused", "nudge not known: it may have landed; nothing was resent");
    } finally {
      inFlight.current = false;
    }
  }

  // holdStep holds a step and shows its landed line, or its failure.
  async function holdStep(hold: () => Promise<void>): Promise<void> {
    clearLines();
    try {
      await hold();
      setNote("interrupt landed");
    } catch (failure) {
      stepFailed(failure, "interrupt refused", "interrupt not known");
    }
  }

  // stopStep closes the Stop question and stops a step, showing its stopped line, or its failure.
  async function stopStep(stop: () => Promise<void>): Promise<void> {
    setStopOpen(false);
    clearLines();
    try {
      await stop();
      setNote("stopped");
    } catch (failure) {
      stepFailed(failure, "stop refused", "stop not known");
    }
  }

  async function submit(): Promise<void> {
    if (stepCalls !== undefined) {
      await nudgeStep();
      return;
    }
    if (inFlight.current || text.trim() === "") {
      return;
    }
    flash();
    const sent = text;
    const pendingSend = failed.current;
    const promptId = pendingSend !== null && pendingSend.text === sent ? pendingSend.promptId : newPromptId();
    inFlight.current = true;
    setError("");
    setNote("");
    onSending?.({ text: sent, promptId });
    if (state === "working") {
      setLocal((current) => [...current, { promptId, text: sent, after: items.at(-1), wasQueued: false }]);
    }
    try {
      await send(sent, promptId);
      failed.current = null;
      setWaitsCleared(pendingKey(pending));
      // Text typed while the send was in flight stays in the box.
      setText((current) => (current === sent ? "" : current));
    } catch (failure) {
      failed.current = { text: sent, promptId };
      unsentLine(failure);
      onUnsent?.(promptId);
      setLocal((current) => current.filter((entry) => entry.promptId !== promptId));
    } finally {
      inFlight.current = false;
    }
  }

  // pullBack puts the pulled text in the box, and shows failure as the error line when the pull is refused.
  async function pullBack(failure: string): Promise<void> {
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
      setError(failure);
    }
  }

  // sendNowRow interrupts the working turn and says each text it cancelled again. The texts that were not said go back
  // into the box after the draft, with their note.
  async function sendNowRow(interruptTurn: () => Promise<string[]>): Promise<void> {
    if (sendNowInFlight.current) {
      return;
    }
    sendNowInFlight.current = true;
    setSendingNow(true);
    setError("");
    setNote("");
    try {
      const { held } = await sendNow(interruptTurn, send, newPromptId);
      if (held.length > 0) {
        append(held);
        setNote(dockLines.sendNowHeld(held.length));
      }
    } catch (failure) {
      unsentLine(failure);
    } finally {
      sendNowInFlight.current = false;
      setSendingNow(false);
    }
  }

  // withdrawRow takes a session's queued message back through take. Its texts go to the box; none means Claude already
  // took the message in, and a refused call is shown as its own note on the row. The row stays until the stream drops it.
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
      void pullBack(dockLines.takeAllBackFailed);
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
  const paneRows = queued.map((message, index) => ({
    key: `queued-${index}`,
    text: message.text,
    canceled: message.canceled,
  }));
  const localRows = sentHere
    .filter((entry) => !sessionQueue.some((message) => message.promptId === entry.promptId))
    .filter((entry) => !queued.some((message) => !message.canceled && sameText(message.text, entry.text)))
    .map((entry) => ({ key: `local-${entry.promptId}`, text: entry.text }));
  // A session's canceled message draws nothing: only the queued ones and the discarded ones are drawn.
  const sessionRows = sessionQueue.filter((message) => message.state === "queued");
  const undelivered = sessionQueue.filter(
    (message) => message.state === "discarded" && !putBackIds.includes(message.promptId),
  );
  // The facts of each drawn row, in the order drawn, for the tray's head.
  const drawn: TrayRowFacts[] = [
    ...paneRows.map((row) => ({ waits: "", cancelled: row.canceled })),
    ...sessionRows.map((message) => ({ waits: message.waits, cancelled: false })),
    ...localRows.map(() => ({ waits: "", cancelled: false })),
    ...undelivered.map(() => ({ waits: "", cancelled: true })),
  ];
  const trayKind = facts.kind === "session" ? "session" : "pane";
  // The waits line is drawn until a send resolves, and again once the pending events change.
  const waits = waitsCleared === pendingKey(pending) ? "" : waitsLine(pending, facts.putOff);
  const shownAbout = about !== undefined && about !== aboutCleared ? about : null;

  // trayRow draws one row: its label when it is cancelled, its text, its control and its note, when it has them.
  const trayRow = (key: string, rowText: string, cancelled: boolean, control: JSX.Element | null, rowNote: string) => (
    <div key={key} class={cancelled ? "tray-row you cancelled" : "tray-row you"}>
      {cancelled && <span class="lbl">{dockLines.notDelivered}</span>}
      <span class="text wrap">
        <ReplyText text={rowText} />
      </span>
      {control !== null && <span class="end">{control}</span>}
      {rowNote !== "" && <span class="note">{rowNote}</span>}
    </div>
  );

  return (
    <div class="dock">
      <div class={`dock-box${flashing ? " flash" : ""}${focused ? " focused" : ""}`} data-state={line.tone}>
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
            {!stopOpen && line.control === "interrupt" && (stepCalls !== undefined || onInterrupt !== undefined) && (
              <Ctl
                label="Interrupt"
                icon="stop"
                tone="bad"
                kbd={stepCalls === undefined ? "esc" : undefined}
                onClick={stepCalls === undefined ? onInterrupt : () => void holdStep(stepCalls.hold)}
              />
            )}
            {line.control === "interrupting" && <Ctl label="Interrupting…" disabled />}
            {line.control === "resume" && resume !== undefined && (
              <Ctl label="Resume" icon="play" tone="go" onClick={() => void resumeRow(resume)} />
            )}
            {stepCalls !== undefined && stopOpen && (
              <span class="confirm">
                <span>{`Stop ${facts.name}?`}</span>
                <Ctl label={`Stop ${facts.name}`} tone="bad" onClick={() => void stopStep(stepCalls.stop)} />
                <Ctl label="Keep it running" onClick={() => setStopOpen(false)} />
              </span>
            )}
            {stepCalls !== undefined && !stopOpen && (
              <Ctl
                only
                label="Stop this step…"
                icon="more"
                expanded={stopOpen}
                onClick={() => setStopOpen((open) => !open)}
              />
            )}
          </span>
        </div>
        {waits !== "" && (
          <div class="dock-waits">
            <LineIcon name="bell" />
            <span>{waits}</span>
          </div>
        )}
        {drawn.length > 0 && (
          <div class="tray">
            <div class="tray-head">
              <span class="lbl">{trayLabel(drawn.length)}</span>
              <span class="why">{trayWhy(trayKind, drawn)}</span>
              <span class="end">
                {pull !== null && drawn.some((row) => !row.cancelled) && (
                  <Ctl label="Take all back" icon="edit" onClick={() => void pullBack(dockLines.takeAllBackFailed)} />
                )}
                {interrupt !== undefined && state === "working" && (
                  <Ctl label="Send now" icon="send" disabled={sendingNow} onClick={() => void sendNowRow(interrupt)} />
                )}
              </span>
            </div>
            {paneRows.map((row) => trayRow(row.key, row.text, row.canceled, null, ""))}
            {sessionRows.map((message) => {
              const outcome = outcomes.get(message.promptId);
              const rowNote =
                outcome === "took" ? dockLines.alreadyTook : outcome === "failed" ? dockLines.takeBackFailed : "";
              const control =
                withdraw !== undefined && outcome !== "took" ? (
                  <Ctl
                    only
                    label="Take this back into the box"
                    icon="edit"
                    onClick={() => void withdrawRow(withdraw, message.promptId)}
                  />
                ) : null;
              return trayRow(`session-${message.promptId}`, message.text, false, control, rowNote);
            })}
            {localRows.map((row) => trayRow(row.key, row.text, false, null, ""))}
            {undelivered.map((message) =>
              trayRow(
                `undelivered-${message.promptId}`,
                message.text,
                true,
                <Ctl
                  only
                  label="Put back in the box"
                  icon="edit"
                  onClick={() => putBackRow(message.text, message.promptId)}
                />,
                "",
              ),
            )}
          </div>
        )}
        {shownAbout !== null && (
          <div class="quote">
            <LineIcon name="chat" />
            <span class="text">
              {"about "}
              {aboutId !== undefined && (
                <>
                  <span class="idc" data-k={aboutId.charAt(0)}>
                    {aboutId}
                  </span>{" "}
                </>
              )}
              {shownAbout}
            </span>
            <Ctl
              only
              label="Not about this"
              icon="close"
              onClick={() => {
                setAboutCleared(shownAbout);
                onClearAbout?.();
              }}
            />
          </div>
        )}
        <div class="dock-input">
          <textarea
            ref={inputRef}
            placeholder={placeholder(facts, narrow)}
            rows={1}
            value={text}
            onInput={(event) => setText(event.currentTarget.value)}
            onKeyDown={onKeyDown}
            onFocus={() => setFocused(true)}
            onBlur={() => setFocused(false)}
          />
          <button type="button" class="go" aria-label="send" onClick={() => void submit()}>
            ↑
          </button>
        </div>
      </div>
      {error !== "" && (
        <p class="inote error" role="alert">
          <LineIcon name="warn" />
          <span>{error}</span>
        </p>
      )}
      {note !== "" && (
        <p class="inote" role="status">
          {note}
        </p>
      )}
    </div>
  );
}
