import type { JSX, Ref } from "preact";
import { useRef, useState } from "preact/hooks";

import { useFlash } from "./flash";
import type { WaitingMessage } from "./gen/orrery/v1/orrery_pb";
import { ReplyText } from "./text";

const sendFailedLine = "Not sent. Your draft is kept; press Enter to try again.";
const pullFailedLine = "Could not pull the message back.";

interface FailedSend {
  text: string;
  promptId: string;
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

// placeholderFor says what sending does in a state: working queues the text for Claude's next step, waiting answers,
// and any other state replies.
function placeholderFor(state: string, label: string): string {
  switch (state) {
    case "working":
      return `Queue for ${label}; it lands at Claude's next step`;
    case "waiting":
      return `Answer ${label}`;
    default:
      return `Reply to ${label}`;
  }
}

// Composer is the send box under the reader. Enter and the send button send the text once; a failed send keeps the
// draft and its prompt id and is never resent by the composer. Up in an empty box pulls the newest queued text back.
// The queued messages draw above the box, oldest first, each as a tag and its text. Its edge and placeholder follow
// the conversation's state.
export function Composer(props: {
  label: string; // the row's label, named in the placeholder
  state: string; // the conversation's state: working, waiting, or any other
  send: (text: string, promptId: string) => Promise<void>;
  pull: (() => Promise<string>) | null; // null: no pull for this row (sessions)
  newPromptId: () => string;
  inputRef?: Ref<HTMLTextAreaElement>; // the page focuses it on `i`
  queued?: WaitingMessage[]; // the row's waiting and canceled messages, oldest first; none drawn when absent
}): JSX.Element {
  const { label, state, send, pull, newPromptId, inputRef, queued = [] } = props;
  const [text, setText] = useState("");
  const [error, setError] = useState("");
  const inFlight = useRef(false);
  const failed = useRef<FailedSend | null>(null);
  const [flashing, flash] = useFlash();

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
    try {
      await send(sent, promptId);
      failed.current = null;
      // Text typed while the send was in flight stays in the box.
      setText((current) => (current === sent ? "" : current));
    } catch {
      failed.current = { text: sent, promptId };
      setError(sendFailedLine);
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

  function onKeyDown(event: KeyboardEvent): void {
    if (event.key === "Enter") {
      event.preventDefault();
      void submit();
    } else if (event.key === "ArrowUp" && text === "" && pull !== null) {
      event.preventDefault();
      void pullBack();
    }
  }

  return (
    <div class="compose">
      {queued.length > 0 && (
        <ol class="queue">
          {queued.map((message, index) => (
            <li key={`queued-${index}`} class={message.canceled ? "queued canceled" : "queued"}>
              <span class="tag">{message.canceled ? "canceled" : "waiting"}</span>
              <span class="text">
                <ReplyText text={message.text} />
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
