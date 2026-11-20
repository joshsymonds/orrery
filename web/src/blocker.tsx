import type { JSX } from "preact";
import { useEffect, useState } from "preact/hooks";

import type { EpicQuestion } from "./gen/orrery/v1/epics_pb";

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

// isTextBox reports whether an element is a text box, whose keys are text and no command.
function isTextBox(element: Element | null): boolean {
  return element instanceof HTMLElement && (element.tagName === "TEXTAREA" || element.tagName === "INPUT");
}

// QuestionCard draws one open question as its option cards: a card per declared choice, in order, then Something
// else, which reveals a text box. The recommended choice is marked with its why and starts selected; a question that
// recommends none starts with nothing selected. Confirm names the selected card and stays disabled while nothing is
// selected, or while Something else is selected with blank text. It sends the choice's id with its does as the text,
// or other with the trimmed text. With numberKeys on, a digit key selects the card it names, unless a text box has
// focus. A returned error shows under the card and keeps the selection and the text; an empty return clears the text.
export function QuestionCard({
  question,
  numberKeys,
  onAnswer,
}: {
  question: EpicQuestion;
  numberKeys: boolean;
  onAnswer: (questionId: string, choice: string, text: string) => Promise<string>;
}): JSX.Element {
  const { choices, recommendation } = question;
  // The card after the last choice is Something else, at the index the choices' count gives.
  const other = choices.length;
  const recommended =
    recommendation === undefined ? -1 : choices.findIndex((choice) => choice.id === recommendation.choice);
  const [picked, setPicked] = useState<number | null>(recommended >= 0 ? recommended : null);
  const [text, setText] = useState("");
  const [error, setError] = useState("");

  const titleOf = (index: number): string => (index === other ? "Something else" : choices[index].id);
  const confirmDisabled = picked === null || (picked === other && text.trim() === "");

  // A digit key picks the card it names. The keys are off once the page is closed, and a text box keeps its keys.
  useEffect(() => {
    if (!numberKeys) {
      return undefined;
    }
    const listener = (event: KeyboardEvent) => {
      if (event.ctrlKey || event.metaKey || event.altKey || isTextBox(document.activeElement)) {
        return;
      }
      if (/^[1-9]$/.test(event.key) && Number(event.key) - 1 <= other) {
        setPicked(Number(event.key) - 1);
      }
    };
    document.addEventListener("keydown", listener);
    return () => document.removeEventListener("keydown", listener);
  }, [numberKeys, other]);

  async function submit(): Promise<void> {
    if (picked === null) {
      return;
    }
    const sent = text;
    const answer =
      picked === other
        ? { choice: "other", text: sent.trim() }
        : { choice: choices[picked].id, text: choices[picked].does };
    let result: string;
    try {
      result = await onAnswer(question.id, answer.choice, answer.text);
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
      <p class="question">{question.text}</p>
      <div class="opts">
        {choices.map((choice, index) => (
          <button
            key={index}
            type="button"
            class={index === picked ? "opt sel" : "opt"}
            aria-pressed={index === picked}
            onClick={() => setPicked(index)}
          >
            <kbd>{index + 1}</kbd>
            <span>
              {index === recommended && <em>recommended</em>}
              <b>{choice.id}</b>
              <small>{choice.does}</small>
              {index === recommended && <small class="why">{recommendation?.why}</small>}
            </span>
          </button>
        ))}
        <button
          type="button"
          class={other === picked ? "opt sel" : "opt"}
          aria-pressed={other === picked}
          onClick={() => setPicked(other)}
        >
          <kbd>{other + 1}</kbd>
          <span>
            <b>Something else</b>
          </span>
        </button>
      </div>
      {picked === other && (
        <textarea aria-label="answer" value={text} onInput={(event) => setText(event.currentTarget.value)} />
      )}
      <button type="submit" class="act primary" disabled={confirmDisabled}>
        {picked === null ? "Answer" : `Answer with ${titleOf(picked)}`}
      </button>
      {error !== "" && (
        <p class="error" role="alert">
          {error}
        </p>
      )}
    </form>
  );
}
