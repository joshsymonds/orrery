import { create } from "@bufbuild/protobuf";
import type { JSX } from "preact";

import { AnswerForm, AskFirst } from "./blocker";
import type { EpicCommands } from "./epicpage";
import { EpicQuestionSchema } from "./gen/orrery/v1/epics_pb";
import type { Need } from "./needs";

// NeedEntry draws one epic need: its sentence, which opens the epic on click, then its control. A question draws the
// answer form for that question, a park draws Retry, and a release need draws Retry and Stop…, which asks first. Any
// other need draws its sentence alone.
export function NeedEntry({
  need,
  slug,
  commands,
  onOpen,
}: {
  need: Need;
  slug: string;
  commands: EpicCommands;
  onOpen: () => void;
}): JSX.Element {
  return (
    <div class="need-entry">
      <p class="need-text" onClick={() => onOpen()}>
        {need.text}
      </p>
      {need.kind === "question" && (
        <AnswerForm
          key={need.question}
          question={create(EpicQuestionSchema, { id: need.question })}
          hideQuestion
          onAnswer={commands.answer}
        />
      )}
      {need.kind === "park" && (
        <button type="button" class="act" onClick={() => commands.retry()}>
          Retry
        </button>
      )}
      {need.kind === "release" && (
        <>
          <button type="button" class="act" onClick={() => commands.retry()}>
            Retry
          </button>
          <AskFirst
            label="Stop…"
            question={`Stop ${slug}?`}
            confirm={`Stop ${slug}`}
            keep="Keep it running"
            onConfirm={() => commands.stop()}
          />
        </>
      )}
    </div>
  );
}
