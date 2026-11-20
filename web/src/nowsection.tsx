import type { JSX } from "preact";

import { AskFirst, QuestionCard } from "./blocker";
import type { EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import { recordTasksOf } from "./record";
import { type PipTone, Pips } from "./pips";
import { askingOf, nowSentenceOf, stepLinesOf } from "./recordwords";
import { clock } from "./workline";

// A step with no task, or a task the record does not list, draws five unreached pips.
const unreached: readonly PipTone[] = ["none", "none", "none", "none", "none"];

// NowSection is the record's Now: its one sentence, what asks placed first under it, and each running step as a row. An
// open question draws as its Ask, an owner's park as its reason with Retry and Stop that asks first, a planner's park as
// one quiet line, and every running step limited as Resume now. Nothing here reads the clock: now is a prop.
export function NowSection({
  detail,
  now,
  numberKeys,
  onAnswer,
  onRetry,
  onStop,
  onResume,
}: {
  detail: EpicsGetResponse;
  now: number;
  numberKeys: boolean;
  onAnswer: (questionId: string, choice: string, text: string) => Promise<string>;
  onRetry: () => void;
  onStop: () => void;
  onResume: () => void;
}): JSX.Element {
  const asking = askingOf(detail);
  const tasks = recordTasksOf(detail);
  const slug = detail.epic?.slug ?? "";
  return (
    <section class="now" aria-label="Now">
      <h3>Now</h3>
      <p class="now-sentence">{nowSentenceOf(detail)}</p>
      {asking?.kind === "question" && (
        <QuestionCard
          key={asking.question.id}
          question={asking.question}
          numberKeys={numberKeys}
          onAnswer={onAnswer}
        />
      )}
      {asking?.kind === "park" && (
        <div class="ask epic park">
          <p class="ask-head">It asks you what to do</p>
          <p class="reason">{asking.reason}</p>
          <button type="button" class="act" onClick={() => onRetry()}>
            Retry
          </button>
          <AskFirst
            label="Stop…"
            question={`Stop ${slug}?`}
            confirm={`Stop ${slug}`}
            keep="Keep it running"
            onConfirm={onStop}
          />
        </div>
      )}
      {asking?.kind === "planner" && <p class="inote quiet">{asking.line}</p>}
      {asking?.kind === "limited" && (
        <button
          type="button"
          class="act"
          title="Try before the reset. A step still over the limit stays held."
          onClick={() => onResume()}
        >
          Resume now
        </button>
      )}
      <div class="tbl steps">
        {stepLinesOf(detail).map((line) => (
          <div key={line.id} class={`tr t-${line.glyph}`}>
            <span class={`g g-${line.glyph}`} aria-label={line.glyph} />
            <span class="strong">{line.label}</span>
            <Pips cells={tasks.find((task) => task.task === line.task)?.cells ?? unreached} />
            <span class="doing">{line.doing}</span>
            <span class="end">{clock(now - line.since)}</span>
          </div>
        ))}
      </div>
    </section>
  );
}
