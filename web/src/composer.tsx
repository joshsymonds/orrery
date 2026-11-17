import type { JSX, Ref } from "preact";
import { useLayoutEffect, useRef, useState } from "preact/hooks";

import { useFlash } from "./flash";
import type { HeldMessage, WaitingMessage } from "./gen/orrery/v1/orrery_pb";
import { type SentText, sameText, withoutTexts } from "./sending";
import { ReplyText } from "./text";

const sendFailedLine = "Not sent. Your draft is kept; press Enter to try again.";
const pullFailedLine = "Could not pull the message back.";
const queuedWords = "It sends when this turn ends";

interface FailedSend {
  text: string;
  promptId: string;
}

// A message this composer sent while its conversation was working. It stays above the box until the turn ends or its
// send fails.
interface LocalQueued {
  promptId: string;
  text: string;
}

// QueueRow is one line of the queue above the box: a message the conversation holds or this composer sent.
interface QueueRow {
  key: string;
  text: string;
  canceled: boolean;
}

// edgeClass is the edge a composer shows for its conversation's state: working, waiting, or idle for any other state.
function edgeClass(state: string): string {
  switch (state) {
    case "working":
      return "edge-working";
    case "waiting":
      return "edge-waiting";
    default:
      return "edge-idle";
  }
}

// placeholderFor says what sending does in a state: working queues the text until the turn ends, waiting answers, and
// any other state replies.
function placeholderFor(state: string, label: string): string {
  switch (state) {
    case "working":
      return `Queue for ${label}; it sends when this turn ends`;
    case "waiting":
      return `Answer ${label}`;
    default:
      return `Reply to ${label}`;
  }
}

// ComposerHandle is what the page calls on a composer when its row's Escape takes queued messages back. append puts
// their texts in the box after its text, joined by blank lines. forget drops the local entry each text matches, since
// the held set no longer carries those messages.
export interface ComposerHandle {
  append: (texts: string[]) => void;
  forget: (texts: string[]) => void;
}

// Composer is the send box under the reader. Enter and the send button send the text once; Shift+Enter or Alt+Enter
// puts a line break at the cursor instead. A send reports itself to onSending as it starts, and to onUnsent if it
// rejects. A failed send keeps the draft and its prompt id and is never resent by the composer. Up in an empty box pulls
// the newest queued text back. In an empty box with no modifier held, Page Up and Page Down go to onPage, and / goes to
// onFind. The queue draws above the box under one head that counts its rows, oldest first, each message as its text:
// the conversation's waiting messages, then its held messages, then the messages this composer sent while the
// conversation was working, except those the held set now carries under their prompt id. A canceled message is tagged.
// Its edge and placeholder follow the conversation's state.
export function Composer(props: {
  label: string; // the row's label, named in the placeholder
  state: string; // the conversation's state: working, waiting, or any other
  send: (text: string, promptId: string) => Promise<void>;
  pull: (() => Promise<string>) | null; // null: no pull for this row (sessions)
  newPromptId: () => string;
  inputRef?: Ref<HTMLTextAreaElement>; // the page focuses it on `i`
  register?: (handle: ComposerHandle | null) => void; // gets the handle while mounted, and null once unmounted
  queued?: WaitingMessage[]; // the row's waiting and canceled messages, oldest first; none drawn when absent
  held?: HeldMessage[]; // the row's held messages, oldest first, each drawn as a queued row; none drawn when absent
  onSending?: (entry: SentText) => void; // called as a send starts, before it is awaited
  onUnsent?: (promptId: string) => void; // called when a send rejects
  onPage?: (direction: "up" | "down") => boolean; // an empty box's Page Up or Page Down; true prevents the key
  onFind?: () => void; // an empty box's /; the key is always prevented, so no slash is typed
}): JSX.Element {
  const {
    label,
    state,
    send,
    pull,
    newPromptId,
    inputRef,
    register,
    queued = [],
    held = [],
    onSending,
    onUnsent,
    onPage,
    onFind,
  } = props;
  const [text, setText] = useState("");
  const [error, setError] = useState("");
  const [local, setLocal] = useState<LocalQueued[]>([]);
  const inFlight = useRef(false);
  const failed = useRef<FailedSend | null>(null);
  const [flashing, flash] = useFlash();
  // caret is the cursor a line break left for the render to park: setting the value moves the cursor to the end.
  const caret = useRef<{ box: HTMLTextAreaElement; at: number } | null>(null);

  // The page holds the handle while this composer is mounted, and drops it when the composer unmounts.
  useLayoutEffect(() => {
    register?.({
      append: (texts) => {
        const returned = texts.join("\n\n");
        setText((current) => (current === "" ? returned : `${current}\n\n${returned}`));
      },
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
      setLocal((current) => [...current, { promptId, text: sent }]);
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

  // The local messages show only while the conversation is working. A message the conversation's queue already holds,
  // not canceled, is drawn once, from the queue, and so is one the held set carries under its prompt id, from the held
  // set.
  const sentHere = state === "working" ? local : [];
  const rows: QueueRow[] = [
    ...queued.map((message, index) => ({ key: `queued-${index}`, text: message.text, canceled: message.canceled })),
    ...held.map((message) => ({ key: `held-${message.promptId}`, text: message.text, canceled: false })),
    ...sentHere
      .filter((entry) => !held.some((message) => message.promptId === entry.promptId))
      .filter((entry) => !queued.some((message) => !message.canceled && sameText(message.text, entry.text)))
      .map((entry) => ({ key: `local-${entry.promptId}`, text: entry.text, canceled: false })),
  ];

  return (
    <div class="compose">
      {rows.length > 0 && (
        <ol class="queue">
          <li class="queue-head">{`${rows.length} queued`}</li>
          {rows.map((row) => (
            <li key={row.key} class={row.canceled ? "queued canceled" : "queued"}>
              {row.canceled && <span class="tag">canceled</span>}
              {!row.canceled && <span class="words">{queuedWords}</span>}
              <span class="text">
                <ReplyText text={row.text} />
              </span>
            </li>
          ))}
        </ol>
      )}
      <div class={`composer ${edgeClass(state)}${flashing ? " flash" : ""}`}>
        <textarea
          ref={inputRef}
          placeholder={placeholderFor(state, label)}
          rows={1}
          value={text}
          onInput={(event) => setText(event.currentTarget.value)}
          onKeyDown={onKeyDown}
        />
        <button type="button" class="go" aria-label="send" onClick={() => void submit()}>
          ↑
        </button>
      </div>
      {error !== "" && (
        <p class="composer-error" role="alert">
          {error}
        </p>
      )}
    </div>
  );
}
