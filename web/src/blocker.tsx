import type { JSX } from "preact";
import { useState } from "preact/hooks";

import type { EpicQuestion } from "./gen/orrery/v1/epics_pb";
import type { Wedge } from "./phases";
import type { Blocker } from "./status";

const answerFailedLine = "Not answered. Your answer is kept.";

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

// AnswerForm answers one open question. Answer stays disabled while the trimmed answer is empty and sends the trimmed
// answer. A returned error shows under the form and keeps the answer; an empty return clears it. hideQuestion leaves
// out the question's own line, for a caller that already draws the question.
export function AnswerForm({
  question,
  hideQuestion = false,
  onAnswer,
}: {
  question: EpicQuestion;
  hideQuestion?: boolean;
  onAnswer: (questionId: string, text: string) => Promise<string>;
}): JSX.Element {
  const [text, setText] = useState("");
  const [error, setError] = useState("");

  async function submit(): Promise<void> {
    const sent = text;
    const answer = sent.trim();
    if (answer === "") {
      return;
    }
    let result: string;
    try {
      result = await onAnswer(question.id, answer);
    } catch {
      result = answerFailedLine;
    }
    setError(result);
    if (result === "") {
      // Text typed while the answer was in flight stays in the box.
      setText((current) => (current === sent ? "" : current));
    }
  }

  return (
    <form
      class="answer"
      onSubmit={(event) => {
        event.preventDefault();
        void submit();
      }}
    >
      {!hideQuestion && <p class="question">{question.text}</p>}
      <textarea
        aria-label="answer"
        value={text}
        onInput={(event) => setText(event.currentTarget.value)}
      />
      <button type="submit" class="act primary" disabled={text.trim() === ""}>
        Answer
      </button>
      {error !== "" && (
        <p class="error" role="alert">
          {error}
        </p>
      )}
    </form>
  );
}

// NowBlock is the now sentence over the epic's blocker. An open question draws as a form, a park draws with Retry, and
// any blocker has a Stop that asks first.
export function NowBlock({
  sentence,
  blocker,
  slug,
  onAnswer,
  onRetry,
  onStop,
}: {
  sentence: string;
  blocker: Blocker | null;
  slug: string;
  onAnswer: (questionId: string, text: string) => Promise<string>;
  onRetry: () => void;
  onStop: () => void;
}): JSX.Element {
  return (
    <section class="now-block">
      {sentence !== "" && <p class="now">{sentence}</p>}
      {blocker?.kind === "question" && (
        <AnswerForm key={blocker.question.id} question={blocker.question} onAnswer={onAnswer} />
      )}
      {blocker?.kind === "parked" && (
        <>
          <p class="blocked">{blocker.text}</p>
          <button type="button" class="act" onClick={() => onRetry()}>
            Retry
          </button>
        </>
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
