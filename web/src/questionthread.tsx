import type { JSX } from "preact";
import { useState } from "preact/hooks";

import type { DesignLogLine, DesignQuestion } from "./gen/orrery/v1/designs_pb";
import { noticeClock } from "./noticewords";
import { whoOfLog, whoOfMaker } from "./stagewho";

// QuestionPlace is where a question's block is drawn: in its design, or in the conversation that asked it.
export type QuestionPlace = "design" | "conversation";

// QuestionAnswer is what a question's box or Accept sends: the box's words, or accept, which takes the recommendation.
export type QuestionAnswer = { accept: true } | { words: string };

export interface QuestionThreadProps {
  question: DesignQuestion;
  // lines are the design's log lines that name the question, oldest first.
  lines: DesignLogLine[];
  place: QuestionPlace;
  onAnswer: (answer: QuestionAnswer) => Promise<void>;
}

// answerFailedLine is a refused answer's words when its call fails without a message: the box keeps its words, so the
// answer can be sent again.
const answerFailedLine = "Not answered. Your answer is kept.";

// Thread draws a question's messages, oldest first: each line's who, its time in code, what was done, and its words.
function Thread({ lines }: { lines: DesignLogLine[] }): JSX.Element {
  return (
    <div class="qt-thread">
      {lines.map((line) => (
        <div key={String(line.seq)} class="qt-msg">
          <span class="qt-who">{whoOfLog(line.maker?.kind ?? "")}</span>
          <code>{noticeClock(line.time)}</code>
          <span class="qt-what">{line.what}</span>
          {line.words !== "" && <span class="qt-words">{line.words}</span>}
        </div>
      ))}
    </div>
  );
}

// QuestionThread draws one design question as its own small conversation. While it is open it draws the question, each
// option as a row with the recommended one marked, the thread, and its own answer box with Send and Accept. A press on
// an option's row puts that option in the box. Once it is settled it draws one line, with its thread folded behind a
// disclosure, and no box, no Accept and no option.
export function QuestionThread({ question, lines, place, onAnswer }: QuestionThreadProps): JSX.Element {
  const [words, setWords] = useState("");
  const [sending, setSending] = useState(false);
  const [failure, setFailure] = useState("");

  const id = `Q${question.id}`;
  const anchor = place === "design" ? id : undefined;
  const settled = question.answered !== 0n;

  // answer sends one answer and holds the box and both buttons until the call settles. A refused call shows its
  // message, and the box keeps its words.
  async function answer(call: QuestionAnswer): Promise<void> {
    if (sending) {
      return;
    }
    setSending(true);
    setFailure("");
    try {
      await onAnswer(call);
    } catch (error) {
      setFailure(error instanceof Error && error.message !== "" ? error.message : answerFailedLine);
    }
    setSending(false);
  }

  if (settled) {
    const shown = question.accepted ? question.answer : `“${question.answer}”`;
    return (
      <section class="ask folded settled" aria-label={`question ${id}`} id={anchor}>
        <span class="lbl">Settled</span>
        <code>{id}</code>
        <b>{shown}</b>
        {question.accepted && <span class="ink-3">· accepted the recommendation</span>}
        <span class="ink-3 who">· {whoOfMaker(question.maker)}</span>
        {lines.length > 0 && (
          <details>
            <summary>
              {lines.length} {lines.length === 1 ? "message" : "messages"} about it
            </summary>
            <Thread lines={lines} />
          </details>
        )}
      </section>
    );
  }

  return (
    <section class="ask qthread" aria-label={`question ${id}`} id={anchor}>
      <div class="head">
        <span class="lbl">Asks you</span>
        <code>{id}</code>
      </div>
      <p class="q">{question.text}</p>
      {question.options.length > 0 && (
        <div class="opts">
          {question.options.map((option, index) => (
            <button key={index} type="button" class="opt" onClick={() => setWords(option)}>
              <span class="nm">{option}</span>
              {option === question.recommendation && <span class="chip">recommended</span>}
            </button>
          ))}
        </div>
      )}
      {lines.length > 0 && <Thread lines={lines} />}
      <div class="field area">
        <textarea
          aria-label="answer"
          placeholder={`Answer ${id}, or ask about it`}
          value={words}
          onInput={(event) => setWords(event.currentTarget.value)}
        />
      </div>
      <div class="foot">
        <button
          type="button"
          class="btn primary"
          disabled={sending || words.trim() === ""}
          onClick={() => void answer({ words: words.trim() })}
        >
          Send
        </button>
        <button
          type="button"
          class="btn"
          disabled={sending || question.recommendation === ""}
          title="Takes the recommendation. The same as typing it."
          onClick={() => void answer({ accept: true })}
        >
          Accept
        </button>
        {sending && <span class="inote">sending…</span>}
      </div>
      {failure !== "" && (
        <p class="inote error" role="alert">
          <span>{failure}</span>
        </p>
      )}
    </section>
  );
}
