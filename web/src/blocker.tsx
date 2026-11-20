import type { JSX } from "preact";
import { useState } from "preact/hooks";

import { Ask } from "./ask";
import { epicAsk, parkAsk } from "./askmodel";
import type { Epic } from "./gen/orrery/v1/epics_pb";
import type { Wedge } from "./phases";
import type { Blocker } from "./status";

// deskQuery holds at a desk width. Narrower than it, an Ask draws no key caps and its More is a sheet.
const deskQuery = "(min-width: 760px)";

// AskFirst is a command button that asks before it acts. Its first click puts the question, a confirm button and a keep
// button in its place. Confirm calls onConfirm once and returns to the label; keep returns without calling.
export function AskFirst({
  label,
  question,
  confirm,
  keep,
  onConfirm,
}: {
  label: string;
  question: string;
  confirm: string;
  keep: string;
  onConfirm: () => void;
}): JSX.Element {
  const [asking, setAsking] = useState(false);
  if (!asking) {
    return (
      <button type="button" class="act" onClick={() => setAsking(true)}>
        {label}
      </button>
    );
  }
  return (
    <>
      <span class="sure">{question}</span>
      <button
        type="button"
        class="act primary"
        onClick={() => {
          setAsking(false);
          onConfirm();
        }}
      >
        {confirm}
      </button>
      <button type="button" class="act" onClick={() => setAsking(false)}>
        {keep}
      </button>
    </>
  );
}

// nowSentence is the one live sentence of an epic: the task and phase of its first running wedge, and the summary of
// that phase's now slice when it has one. Empty when no wedge runs.
export function nowSentence(wedges: Wedge[]): string {
  const running = wedges.find((wedge) => wedge.tone === "run");
  if (running === undefined) {
    return "";
  }
  const summary = running.slices.find((slice) => slice.state === "now")?.summary ?? "";
  const head = `${running.task} · ${running.place}`;
  return summary === "" ? head : `${head} · ${summary}`;
}

// NowBlock is the now sentence over the epic's blocker. An open question draws as its Ask, which answers on confirm, and a
// park draws as its Ask, whose one answer is Retry. Any blocker has a Stop that asks first. numberKeys is whether a digit
// key picks an option of the question.
export function NowBlock({
  sentence,
  blocker,
  slug,
  epic,
  now,
  numberKeys,
  onAnswer,
  onRetry,
  onStop,
}: {
  sentence: string;
  blocker: Blocker | null;
  slug: string;
  epic: Epic | undefined;
  now: number;
  numberKeys: boolean;
  onAnswer: (questionId: string, choice: string, text: string) => Promise<string>;
  onRetry: () => void;
  onStop: () => void;
}): JSX.Element {
  const narrow = !window.matchMedia(deskQuery).matches;
  const question = blocker?.kind === "question" ? blocker.question : undefined;
  const parked = blocker?.kind === "parked" ? blocker.text : undefined;
  return (
    <section class="now-block">
      {sentence !== "" && <p class="now">{sentence}</p>}
      {question !== undefined && epic !== undefined && (
        <Ask
          key={question.id}
          model={epicAsk(epic, question)}
          now={now}
          phase="open"
          picked={null}
          note=""
          keyCaps
          narrow={narrow}
          numberKeys={numberKeys}
          onConfirm={(choice, text) => onAnswer(question.id, choice, text)}
        />
      )}
      {parked !== undefined && epic !== undefined && (
        <Ask
          key="park"
          model={parkAsk(epic, parked)}
          now={now}
          phase="open"
          picked={null}
          note=""
          keyCaps
          narrow={narrow}
          numberKeys={numberKeys}
          onConfirm={() => {
            onRetry();
            return Promise.resolve("");
          }}
        />
      )}
      {blocker !== null && (
        <AskFirst
          label="Stop…"
          question={`Stop ${slug}?`}
          confirm={`Stop ${slug}`}
          keep="Keep it running"
          onConfirm={onStop}
        />
      )}
    </section>
  );
}
