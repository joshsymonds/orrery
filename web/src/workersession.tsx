import type { JSX } from "preact";
import { useState } from "preact/hooks";

import type { Conversation } from "./conversation";
import type { EpicStep } from "./gen/orrery/v1/epics_pb";
import type { Phase } from "./phases";
import { Reader } from "./reader";
import { StepBox } from "./stepbox";
import { type Crumb, TitleRow } from "./titlerow";

// A step keeps no seen mark. Infinity makes no item newer than it, so the step's reader draws no since-you-looked rule.
const stepSeenAt = Number.POSITIVE_INFINITY;

// WorkerSessionPage is one step's worker session: the title row under orrery, the epic, the task and its phase, then the
// worker's conversation in the reader, then, while the step has not ended, the step box, whose nudge resumes a held
// step. The page draws the conversation it is given; following the stream stays with the app.
export function WorkerSessionPage({
  conversation,
  step,
  epic,
  task,
  phase,
  onGoList,
  onGoEpic,
  onBack,
  nudge,
  hold,
  stop,
}: {
  conversation: Conversation | null;
  step: EpicStep;
  epic: string;
  task: string;
  phase: Phase;
  onGoList: () => void;
  onGoEpic: () => void;
  onBack: () => void;
  nudge: (text: string) => Promise<void>;
  hold: () => Promise<void>;
  stop: () => Promise<void>;
}): JSX.Element {
  const [open, setOpen] = useState(false);
  const crumbs: Crumb[] = [
    { label: "orrery", onGo: onGoList },
    { label: epic, onGo: onGoEpic },
    { label: `${task} · ${phase}`, onGo: onBack },
    { label: "session" },
  ];
  return (
    <div class="worker-session">
      <TitleRow crumbs={crumbs} onBack={onBack} />
      <Reader
        conversation={conversation}
        seenAt={stepSeenAt}
        open={open}
        step={step}
        onToggleOpen={() => setOpen((value) => !value)}
      />
      {step.end === "" && <StepBox label={task} held={step.held} nudge={nudge} hold={hold} stop={stop} />}
    </div>
  );
}
