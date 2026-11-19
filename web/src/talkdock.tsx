import type { JSX } from "preact";
import { useEffect, useMemo, useRef, useState } from "preact/hooks";

import { type Clients, newPromptId } from "./actions";
import { epicCallsOf } from "./card";
import { Composer } from "./composer";
import { type Conversation, shownConversation } from "./conversation";
import { type EpicsView, noEpics } from "./epics";
import { follow } from "./follow";
import { Reader, ReaderHead } from "./reader";
import type { Row } from "./rows";
import { type Sending, sendingEntry, unsentEntries } from "./sending";
import { outcomeOf } from "./stepbox";
import { lastPathElement, listedTitle, TitleParts } from "./titleparts";

// The conversation Talk opens: a pane or a headless session by its id, named by its served label when no listed row does.
interface TalkTarget {
  kind: "pane" | "session";
  id: string;
  label: string;
}

const millisPerSecond = 1000;

// plainTitle is a title as text, for the composer's placeholder: its directory's last element, then its line, or
// whichever is not empty.
function plainTitle(title: { dir: string; line: string }): string {
  return [lastPathElement(title.dir), title.line].filter((part) => part !== "").join(" ");
}

// TalkDock draws the conversation Talk opened as a panel beside the epic page. It follows the target's conversation
// until it unmounts or the target changes, and names it with a × that calls onClose. Its head draws the target's listed
// title through TitleParts, joined at each render, so a headline that refreshes while the dock is open shows at once. Its
// reader and composer draw the conversation as the pinned reader does. While the session is interrupted, Resume resumes
// its turn; a refusal shows its message under the reader. The reader's "since you looked" rule marks what arrives after
// the dock opened on the target. A message sent to the target shows in its reader as sending until its stored prompt
// arrives, and above the box as queued while the target is working or queues it, and as not delivered once it discards
// it. The reader draws an epic a call created as its card, through the openers openEpic and openStep and the epics the
// app's poll has loaded, which the app hands the dock; the reader holds epic calls only when it is given both openers.
export function TalkDock({
  clients,
  rows,
  target,
  onClose,
  openEpic,
  openStep,
  epics = noEpics,
}: {
  clients: Clients;
  rows: Row[];
  target: TalkTarget;
  onClose: () => void;
  openEpic?: (id: string) => void;
  openStep?: (epic: string, step: string) => void;
  epics?: EpicsView;
}): JSX.Element {
  const title = listedTitle(rows, target.kind, target.id, target.label);
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

  // The reader draws the conversation without the prompts its queue still holds or has discarded.
  const shown = useMemo(() => shownConversation(conversation), [conversation]);
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

  async function withdraw(promptId: string): Promise<string[]> {
    const reply = await clients.control.withdraw({ id: target.id, promptId });
    return reply.texts;
  }

  return (
    <aside class="dock" aria-label="talk">
      <ReaderHead label={<TitleParts dir={title.dir} line={title.line} />} where="">
        <button type="button" class="close" aria-label="close" onClick={onClose}>
          ×
        </button>
      </ReaderHead>
      <Reader
        conversation={shown}
        seenAt={seenAt}
        open={false}
        onOlder={() => olderPage.current()}
        sending={unsentEntries(sending, conversation, target.kind)}
        epicCalls={
          openEpic === undefined || openStep === undefined ? undefined : epicCallsOf(clients, openEpic, openStep, epics)
        }
      />
      {interrupted && (
        <button type="button" class="act" onClick={() => void resume()}>
          Resume
        </button>
      )}
      {refusal !== "" && <p class="error">{refusal}</p>}
      <Composer
        label={plainTitle(title)}
        state={state}
        send={send}
        pull={target.kind === "pane" ? pull : null}
        newPromptId={newPromptId}
        queued={conversation?.state?.waiting}
        sessionQueue={conversation?.queued}
        withdraw={target.kind === "session" ? withdraw : undefined}
        onSending={(entry) => setSending((previous) => [...previous, sendingEntry(entry, conversation)])}
        onUnsent={(promptId) => setSending((previous) => previous.filter((entry) => entry.promptId !== promptId))}
      />
    </aside>
  );
}
