import type { JSX, Ref } from "preact";
import { useRef, useState } from "preact/hooks";
import { Code, ConnectError } from "@connectrpc/connect";

export type Refusal = { outcome: "refused"; message: string } | { outcome: "unknown" };

const refusedCodes: ReadonlySet<Code> = new Set([Code.FailedPrecondition, Code.NotFound, Code.InvalidArgument]);

// outcomeOf sorts a failed call. A refused code means the server turned the call down, and its raw message is the
// reason. Any other failure leaves the outcome unknown: the call may have landed.
export function outcomeOf(error: unknown): Refusal {
  const connectError = ConnectError.from(error);
  if (refusedCodes.has(connectError.code)) {
    return { outcome: "refused", message: connectError.rawMessage };
  }
  return { outcome: "unknown" };
}

interface Notice {
  text: string;
  failed: boolean;
}

// failure words a failed call's notice: a refusal shows its message, and any other failure shows the call's unknown line.
function failure(outcome: Refusal, refused: string, unknown: string): Notice {
  return { text: outcome.outcome === "refused" ? `${refused}: ${outcome.message}` : unknown, failed: true };
}

// StepBox is the box under a step's reader: a nudge composer, Interrupt, and Stop. Enter sends the nudge text once; a
// failed nudge keeps the draft and is never resent by the box. Stop asks first: it reveals Confirm stop and Cancel, and
// only Confirm stop calls the server. Every message is drawn as text.
export function StepBox(props: {
  label: string; // placeholder "Nudge <label>"
  nudge: (text: string) => Promise<void>;
  hold: () => Promise<void>;
  stop: () => Promise<void>;
  inputRef?: Ref<HTMLTextAreaElement>; // the page's handle on the textarea
}): JSX.Element {
  const { label, nudge, hold, stop, inputRef } = props;
  const [text, setText] = useState("");
  const [notice, setNotice] = useState<Notice | null>(null);
  const [confirming, setConfirming] = useState(false);
  const inFlight = useRef(false);
  // unknownText is the text of a nudge whose outcome is not known. Enter does not send it again until the text changes.
  const unknownText = useRef<string | null>(null);

  async function submit(): Promise<void> {
    if (inFlight.current || text.trim() === "" || text === unknownText.current) {
      return;
    }
    const sent = text;
    inFlight.current = true;
    setNotice(null);
    try {
      await nudge(sent);
      unknownText.current = null;
      // Text typed while the nudge was in flight stays in the box.
      setText((current) => (current === sent ? "" : current));
      setNotice({ text: "nudge landed", failed: false });
    } catch (error) {
      const outcome = outcomeOf(error);
      unknownText.current = outcome.outcome === "unknown" ? sent : null;
      setNotice(failure(outcome, "nudge refused", "nudge not known: it may have landed; nothing was resent"));
    } finally {
      inFlight.current = false;
    }
  }

  async function interrupt(): Promise<void> {
    setNotice(null);
    try {
      await hold();
      setNotice({ text: "interrupt landed", failed: false });
    } catch (error) {
      setNotice(failure(outcomeOf(error), "interrupt refused", "interrupt not known"));
    }
  }

  async function confirmStop(): Promise<void> {
    setConfirming(false);
    setNotice(null);
    try {
      await stop();
      setNotice({ text: "stopped", failed: false });
    } catch (error) {
      setNotice(failure(outcomeOf(error), "stop refused", "stop not known"));
    }
  }

  function onKeyDown(event: KeyboardEvent): void {
    if (event.key === "Enter") {
      event.preventDefault();
      void submit();
    }
  }

  return (
    <div class="stepbox">
      <div class="composer">
        <textarea
          ref={inputRef}
          placeholder={`Nudge ${label}`}
          rows={1}
          value={text}
          onInput={(event) => setText(event.currentTarget.value)}
          onKeyDown={onKeyDown}
        />
      </div>
      <div class="stepbox-actions">
        <button type="button" onClick={() => void interrupt()}>
          Interrupt
        </button>
        {confirming ? (
          <>
            <button type="button" class="stop-confirm" onClick={() => void confirmStop()}>
              Confirm stop
            </button>
            <button type="button" onClick={() => setConfirming(false)}>
              Cancel
            </button>
          </>
        ) : (
          <button type="button" onClick={() => setConfirming(true)}>
            Stop
          </button>
        )}
      </div>
      {notice !== null && (
        <p class={notice.failed ? "stepbox-notice err" : "stepbox-notice"} role={notice.failed ? "alert" : "status"}>
          {notice.text}
        </p>
      )}
    </div>
  );
}
