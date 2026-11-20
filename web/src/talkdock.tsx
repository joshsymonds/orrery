import type { JSX } from "preact";
import { useEffect, useMemo, useRef, useState } from "preact/hooks";

import { type Clients, newPromptId } from "./actions";
import { epicCallsOf } from "./card";
import { type Conversation, shownConversation } from "./conversation";
import { Dock } from "./dock";
import { rowFacts } from "./dockfacts";
import { type EpicsView, noEpics } from "./epics";
import { follow } from "./follow";
import { Reader, ReaderHead } from "./reader";
import type { Row } from "./rows";
import { type Sending, sendingEntry, unsentEntries } from "./sending";
import { listedTitle, TitleParts } from "./titleparts";

// The conversation Talk opens: a pane or a headless session by its id, named by its served label when no listed row does.
interface TalkTarget {
  kind: "pane" | "session";
  id: string;
  label: string;
}

const millisPerSecond = 1000;

// targetRow is the row a dock follows when no listed row holds its target: a row with only the target's kind, id and
// served label, so its facts name the target by that label.
function targetRow(target: TalkTarget): Row {
  return {
    key: `${target.kind}:${target.id}`,
    id: target.id,
    archiveId: "",
    kind: target.kind,
    label: target.label,
    project: "",
    host: "",
    state: "",
    summary: "",
    question: "",
    questionHash: "",
    changed: 0,
    seen: 0,
    unseen: false,
    archived: false,
    archiveKind: "none",
  };
}

// TalkDock draws the conversation Talk opened as a panel beside the epic page. It follows the target's conversation
// until it unmounts or the target changes, and names it with a × that calls onClose. Its head draws the target's listed
// title through TitleParts, joined at each render, so a headline that refreshes while the dock is open shows at once. Its
// reader draws the conversation as the pinned reader does, and its Dock reads the target's facts: while the session is
// interrupted, the Dock's Resume resumes its turn, and a refusal shows its message under the box. The reader's "since you
// looked" rule marks what arrives after the dock opened on the target. A message sent to the target shows in its reader as
// sending until its stored prompt arrives, and above the box as queued while the target is working or queues it, and as
// not delivered once it discards it. The reader draws an epic a call created as its card, through the openers openEpic and
// openStep and the epics the app's poll has loaded, which the app hands the dock; the reader holds epic calls only when it
// is given both openers.
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
  const listed = rows.find((row) => row.kind === target.kind && row.id === target.id) ?? targetRow(target);
  const [conversation, setConversation] = useState<Conversation | null>(null);
  const [seenAt, setSeenAt] = useState(0);
  // The messages sent to the followed target that its stored conversation does not hold yet.
  const [sending, setSending] = useState<Sending[]>([]);
  // The older page of the followed target, which the reader calls at its top.
  const olderPage = useRef<() => void>(() => {});

  useEffect(() => {
    setConversation(null);
    setSeenAt(Date.now() / millisPerSecond);
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

  async function resume(): Promise<void> {
    await clients.control.resume({ id: target.id });
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
    <aside class="talk-panel" aria-label="talk">
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
      <Dock
        facts={rowFacts(listed, conversation, false)}
        now={Date.now() / millisPerSecond}
        send={send}
        pull={target.kind === "pane" ? pull : null}
        newPromptId={newPromptId}
        queued={conversation?.state?.waiting}
        sessionQueue={conversation?.queued}
        withdraw={target.kind === "session" ? withdraw : undefined}
        resume={target.kind === "session" ? resume : undefined}
        onSending={(entry) => setSending((previous) => [...previous, sendingEntry(entry, conversation)])}
        onUnsent={(promptId) => setSending((previous) => previous.filter((entry) => entry.promptId !== promptId))}
      />
    </aside>
  );
}
