import type { JSX } from "preact";
import { useState } from "preact/hooks";

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
