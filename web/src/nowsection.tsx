import type { JSX } from "preact";

import { Ask } from "./ask";
import { epicAsk, parkAsk } from "./askmodel";
import { AskFirst } from "./blocker";
import type { EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import { pipsOf, recordTasksOf } from "./record";
import { Pips } from "./pips";
import { askingOf, nowSentenceOf, stepLinesOf } from "./recordwords";
import { elapsed } from "./dockwords";

// NowSection is the record's Now: its one sentence, what asks placed first under it, and each running step as a row. An
// open question draws as its Ask, which answers on confirm, and an owner's park as its Ask, whose one answer is Retry,
// then Stop that asks first; a planner's park draws as one quiet line, and every running step limited as Resume now.
// numberKeys is whether a digit key picks an option of the Ask; narrow (under 760px) draws it without key caps. Nothing
// here reads the clock: now is a prop.
export function NowSection({
  detail,
  now,
  narrow,
  numberKeys,
  onAnswer,
  onRetry,
  onStop,
  onResume,
}: {
  detail: EpicsGetResponse;
  now: number;
  narrow: boolean;
  numberKeys: boolean;
  onAnswer: (questionId: string, choice: string, text: string) => Promise<string>;
  onRetry: () => void;
  onStop: () => void;
  onResume: () => void;
}): JSX.Element {
  const asking = askingOf(detail);
  const tasks = recordTasksOf(detail);
  const epic = detail.epic;
  const slug = epic?.slug ?? "";
  return (
    <section class="now" aria-label="Now">
      <h3>Now</h3>
      <p class="now-sentence">{nowSentenceOf(detail)}</p>
      {asking?.kind === "question" && epic !== undefined && (
        <Ask
          key={asking.question.id}
          model={epicAsk(epic, asking.question)}
          now={now}
          phase="open"
          picked={null}
          note=""
          keyCaps
          narrow={narrow}
          numberKeys={numberKeys}
          onConfirm={(choice, text) => onAnswer(asking.question.id, choice, text)}
        />
      )}
      {asking?.kind === "park" && epic !== undefined && (
        <>
          <Ask
            key="park"
            model={parkAsk(epic, asking.reason)}
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
          <div class="doors">
            <AskFirst
              label="Stop…"
              question={`Stop ${slug}?`}
              confirm={`Stop ${slug}`}
              keep="Keep it running"
              onConfirm={onStop}
            />
          </div>
        </>
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
            <Pips cells={pipsOf(line.task, tasks)} />
            <span class="doing wide">{line.doing}</span>
            <span class="end">{elapsed(now - line.since)}</span>
          </div>
        ))}
      </div>
    </section>
  );
}
