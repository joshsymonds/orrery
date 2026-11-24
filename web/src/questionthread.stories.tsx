// The stories of QuestionThread: a question open with its options, its thread and its box; the same question with its
// box typed into, as the owner types in it; and the question settled, folded to one line. Every name and word is invented.

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import type { VNode } from "preact";
import { useEffect, useRef } from "preact/hooks";

import type { Stories } from "./catalog/story";
import { at } from "./fixtures/clock";
import {
  DesignLogLineSchema,
  DesignQuestionSchema,
  RecordMakerSchema,
  type DesignLogLine,
  type DesignQuestion,
} from "./gen/orrery/v1/designs_pb";
import * as drawing from "./questionthread";

const noop = async () => {};

// question is the open question Q1 of the rota design, with its recommended option.
function question(init: MessageInitShape<typeof DesignQuestionSchema> = {}): DesignQuestion {
  return create(DesignQuestionSchema, {
    id: 1n,
    contract: "rota-export",
    text: "Bump the embedded tz database?\n\nThe pinned database lacks the 2026 Lisbon rule.",
    options: ["Keep the pin", "Allow the bump"],
    recommendation: "Keep the pin",
    asked: at("@13:50"),
    ...init,
  });
}

// asked is the conversation's line that asked Q1, with its maker.
const asked: DesignLogLine = create(DesignLogLineSchema, {
  seq: 1n,
  time: at("@13:50"),
  maker: create(RecordMakerSchema, { kind: "conversation" }),
  what: "asked",
  ids: ["Q1"],
});

// said is the owner's message in Q1's thread, which asks back and settles nothing.
const said: DesignLogLine = create(DesignLogLineSchema, {
  seq: 2n,
  time: at("@13:52"),
  maker: create(RecordMakerSchema, { kind: "owner" }),
  what: "said",
  ids: ["Q1"],
  words: "Which of the two keeps the Lisbon case for the rota?",
});

// Typed is the open question with its box typed into: the owner's words are set in the box once it has drawn, as a
// keystroke sets them, and no answer is sent.
function Typed(): VNode {
  const root = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    const box = root.current?.querySelector<HTMLTextAreaElement>('textarea[aria-label="answer"]');
    if (box) {
      box.value = "Allow the bump, and say so in the record.";
      box.dispatchEvent(new Event("input", { bubbles: true }));
    }
  }, []);
  return (
    <div ref={root}>
      <drawing.QuestionThread question={question()} lines={[asked, said]} place="design" onAnswer={noop} />
    </div>
  );
}

export const QuestionThread: Stories = {
  open: () => <drawing.QuestionThread question={question()} lines={[asked]} place="design" onAnswer={noop} />,
  typing: () => <Typed />,
  settled: () => (
    <drawing.QuestionThread
      question={question({
        answer: "Keep the pin",
        accepted: true,
        answered: at("@14:02"),
        maker: create(RecordMakerSchema, { kind: "owner_serve" }),
      })}
      lines={[asked, said]}
      place="design"
      onAnswer={noop}
    />
  ),
};
