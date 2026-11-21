import type { ComponentChildren, JSX } from "preact";
import { useState } from "preact/hooks";

import * as actions from "./actions";
import type { Conversation } from "./conversation";
import { Dock } from "./dock";
import { stepFacts } from "./dockfacts";
import { StepCard, type StepEvent } from "./eventring";
import type { EpicsGetResponse, EpicStep } from "./gen/orrery/v1/epics_pb";
import type { Phase } from "./phases";
import { recordTasksOf } from "./record";
import { clock, Reader } from "./reader";
import { ContextBand } from "./taskband";
import { type Crumb, TitleRow } from "./titlerow";

// A step keeps no seen mark. Infinity makes no item newer than it, so the step's reader draws no since-you-looked rule.
const stepSeenAt = Number.POSITIVE_INFINITY;

// WorkerSessionPage is one step's worker session. Its title row holds the crumbs and no commands; under it the epic's
// band, then the worker's conversation in the reader. While the step has not ended, the step's dock is drawn under the
// reader, labelled with the task's title, and its nudge resumes a held step. Once the step has ended, the dock is gone and
// the step's end is the last block. The page draws the conversation it is given; following the stream stays with the app.
export function WorkerSessionPage({
  conversation,
  now,
  detail,
  step,
  task,
  phase,
  onOpenEpic,
  onOpenTask,
  onBack,
  onOlder,
  nudge,
  hold,
  stop,
  strip,
}: {
  conversation: Conversation | null;
  now: number; // unix seconds, for the band's epic line and the dock's state line
  detail: EpicsGetResponse; // the epic's Get answer, which the band and the task's title read
  step: EpicStep;
  task: string;
  phase: Phase; // the phase the session was opened from, which the task's title opens its record at
  onOpenEpic: () => void;
  onOpenTask: (task: string, phase: Phase) => void;
  onBack: () => void;
  onOlder: () => void;
  nudge: (text: string) => Promise<void>;
  hold: () => Promise<void>;
  stop: () => Promise<void>;
  strip?: ComponentChildren;
}): JSX.Element {
  const [open, setOpen] = useState(false);
  const title = recordTasksOf(detail).find((candidate) => candidate.task === task)?.title ?? task;
  const crumbs: Crumb[] = [
    { label: detail.epic?.slug ?? "", onGo: onOpenEpic },
    { label: title, onGo: () => onOpenTask(task, phase) },
    { label: "Worker session" },
  ];
  return (
    <div class="worker-session">
      <TitleRow crumbs={crumbs} onBack={onBack} strip={strip} />
      <ContextBand detail={detail} now={now} current={task} onOpenEpic={onOpenEpic} onOpenTask={onOpenTask} />
      <Reader
        conversation={conversation}
        seenAt={stepSeenAt}
        open={open}
        step={step}
        onToggleOpen={() => setOpen((value) => !value)}
        onOlder={onOlder}
      />
      {step.end === "" ? (
        <Dock
          facts={stepFacts({ label: title, held: step.held, started: Number(step.started), doing: step.doing }, conversation)}
          now={now}
          send={nudge}
          pull={null}
          newPromptId={actions.newPromptId}
          stepCalls={{ hold, stop }}
        />
      ) : (
        <div class="reader step-end-row">
          <div class="t">
            <span class="gut">{clock(step.ended)}</span>
            <div class="body">
              <StepCard event={stepEndOf(step, detail)} time={step.ended} />
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

// stepEndOf is a step's end as the conversation's step-end block holds it: its id, agent, end, epic and task, and
// its summary as the brief, one line each.
function stepEndOf(step: EpicStep, detail: EpicsGetResponse): StepEvent {
  return {
    step: step.id,
    agent: step.agent,
    end: step.end,
    epic: detail.epic?.id ?? "",
    task: step.task,
    brief: step.summary === "" ? [] : step.summary.split("\n"),
  };
}
