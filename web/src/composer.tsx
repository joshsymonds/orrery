import type { JSX, Ref } from "preact";
import { useRef, useState } from "preact/hooks";

const sendFailedLine = "Not sent. Your draft is kept; press Enter to try again.";
const pullFailedLine = "Could not pull the message back.";

interface FailedSend {
  text: string;
  promptId: string;
}

// Composer is the send box under the reader. Enter sends the text once; a failed send keeps the draft and its
// prompt id and is never resent by the composer. Up in an empty box pulls the newest queued text back.
export function Composer(props: {
  label: string; // placeholder "Reply to <label>"
  send: (text: string, promptId: string) => Promise<void>;
  pull: (() => Promise<string>) | null; // null: no pull for this row (sessions)
  newPromptId: () => string;
  inputRef?: Ref<HTMLTextAreaElement>; // the page focuses it on `i`
}): JSX.Element {
  const { label, send, pull, newPromptId, inputRef } = props;
  const [text, setText] = useState("");
  const [error, setError] = useState("");
  const inFlight = useRef(false);
  const failed = useRef<FailedSend | null>(null);

  async function submit(): Promise<void> {
    if (inFlight.current || text.trim() === "") {
      return;
    }
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
    <div class="composer">
      <textarea
        ref={inputRef}
        placeholder={`Reply to ${label}`}
        rows={1}
        value={text}
        onInput={(event) => setText(event.currentTarget.value)}
        onKeyDown={onKeyDown}
      />
      {error !== "" && (
        <p class="composer-error" role="alert">
          {error}
        </p>
      )}
    </div>
  );
}
