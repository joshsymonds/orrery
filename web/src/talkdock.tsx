import type { JSX } from "preact";
import { useEffect, useRef, useState } from "preact/hooks";

import { type Clients, newPromptId } from "./actions";
import { Composer } from "./composer";
import type { Conversation } from "./conversation";
import { follow } from "./follow";
import { Reader, ReaderHead } from "./reader";
import { type Sending, sendingEntry, unsentEntries } from "./sending";
import { outcomeOf } from "./stepbox";

// The conversation Talk opens: a pane or a headless session by its id, named by its label.
interface TalkTarget {
  kind: "pane" | "session";
  id: string;
  label: string;
}

const millisPerSecond = 1000;

// TalkDock draws the conversation Talk opened as a panel beside the epic page. It follows the target's conversation
// until it unmounts or the target changes, and names it with a × that calls onClose. Its reader and composer draw the
// conversation as the pinned reader does. While the session is interrupted, Resume resumes its turn; a refusal shows
// its message under the reader. The reader's "since you looked" rule marks what arrives after the dock opened on the
// target. A message sent to the target shows in its reader as sending until its stored prompt arrives, and above the box
// as queued while the target is working.
export function TalkDock({
  clients,
  target,
  onClose,
}: {
  clients: Clients;
  target: TalkTarget;
  onClose: () => void;
}): JSX.Element {
  const [conversation, setConversation] = useState<Conversation | null>(null);
  const [seenAt, setSeenAt] = useState(0);
  const [refusal, setRefusal] = useState("");
  // The messages sent to the followed target that its stored conversation does not hold yet.
  const [sending, setSending] = useState<Sending[]>([]);
  // The older page of the followed target, which the reader calls at its top.
  const olderPage = useRef<() => void>(() => {});

  useEffect(() => {
    setConversation(null);
    setSeenAt(Date.now() / millisPerSecond);
    setRefusal("");
    setSending([]);
    const followed = follow(clients, { kind: target.kind, id: target.id }, setConversation);
    olderPage.current = followed.older;
    return followed.stop;
  }, [clients, target.kind, target.id]);

  // A stored prompt that arrives ends its sending entry, so the entry is pruned as the conversation changes.
  useEffect(() => {
    setSending((previous) => unsentEntries(previous, conversation, target.kind));
  }, [conversation, target.kind]);

  const interrupted = conversation?.state?.session?.state === "interrupted";
  const state = conversation?.state?.pane?.state ?? conversation?.state?.session?.state ?? "";

  async function resume(): Promise<void> {
    setRefusal("");
    try {
      await clients.control.resume({ id: target.id });
    } catch (error) {
      const outcome = outcomeOf(error);
      setRefusal(
        outcome.outcome === "refused"
          ? `resume refused: ${outcome.message}`
          : "resume not known: it may have landed",
      );
    }
  }

  async function send(text: string, promptId: string): Promise<void> {
    if (target.kind === "pane") {
      await clients.panes.sendText({ pane: target.id, text });
    } else {
      await clients.control.say({ id: target.id, text, promptId });
    }
  }

  async function pull(): Promise<string> {
    const reply = await clients.panes.pullQueued({ pane: target.id });
    return reply.text;
  }

  return (
    <aside class="dock" aria-label="talk">
      <ReaderHead label={target.label} where="">
        <button type="button" class="close" aria-label="close" onClick={onClose}>
          ×
        </button>
      </ReaderHead>
      <Reader
        conversation={conversation}
        seenAt={seenAt}
        open={false}
        onOlder={() => olderPage.current()}
        sending={unsentEntries(sending, conversation, target.kind)}
      />
      {interrupted && (
        <button type="button" class="act" onClick={() => void resume()}>
          Resume
        </button>
      )}
      {refusal !== "" && <p class="error">{refusal}</p>}
      <Composer
        label={target.label}
        state={state}
        send={send}
        pull={target.kind === "pane" ? pull : null}
        newPromptId={newPromptId}
        queued={conversation?.state?.waiting}
        onSending={(entry) => setSending((previous) => [...previous, sendingEntry(entry, conversation)])}
        onUnsent={(promptId) => setSending((previous) => previous.filter((entry) => entry.promptId !== promptId))}
      />
    </aside>
  );
}
