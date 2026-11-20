import type { ComponentChildren, JSX } from "preact";
import { useState } from "preact/hooks";

import * as actions from "./actions";
import type { Conversation } from "./conversation";
import { Dock } from "./dock";
import { stepFacts } from "./dockfacts";
import type { EpicStep } from "./gen/orrery/v1/epics_pb";
import type { Phase } from "./phases";
import { Reader } from "./reader";
import { type Crumb, type InboxMark, TitleRow } from "./titlerow";

// A step keeps no seen mark. Infinity makes no item newer than it, so the step's reader draws no since-you-looked rule.
const stepSeenAt = Number.POSITIVE_INFINITY;

// WorkerSessionPage is one step's worker session: the title row under the mark, the epic, the task and its phase, then
// the worker's conversation in the reader, then, while the step has not ended, the step's dock, whose nudge resumes a
// held step. The page draws the conversation it is given; following the stream stays with the app.
export function WorkerSessionPage({
  conversation,
  now,
  step,
  epic,
  task,
  phase,
  onGoEpic,
  onBack,
  onOlder,
  nudge,
  hold,
  stop,
  mark,
  strip,
}: {
  conversation: Conversation | null;
  now: number; // unix seconds, for the dock's state line
  step: EpicStep;
  epic: string;
  task: string;
  phase: Phase;
  onGoEpic: () => void;
  onBack: () => void;
  onOlder: () => void;
  nudge: (text: string) => Promise<void>;
  hold: () => Promise<void>;
  stop: () => Promise<void>;
  mark?: InboxMark;
  strip?: ComponentChildren;
}): JSX.Element {
  const [open, setOpen] = useState(false);
  const crumbs: Crumb[] = [
    { label: epic, onGo: onGoEpic },
    { label: `${task} · ${phase}`, onGo: onBack },
    { label: "session" },
  ];
  return (
    <div class="worker-session">
      <TitleRow crumbs={crumbs} onBack={onBack} mark={mark} strip={strip} />
      <Reader
        conversation={conversation}
        seenAt={stepSeenAt}
        open={open}
        step={step}
        onToggleOpen={() => setOpen((value) => !value)}
        onOlder={onOlder}
      />
      {step.end === "" && (
        <Dock
          facts={stepFacts({ label: task, held: step.held, started: Number(step.started) }, conversation)}
          now={now}
          send={nudge}
          pull={null}
          newPromptId={actions.newPromptId}
          stepCalls={{ hold, stop }}
        />
      )}
    </div>
  );
}
