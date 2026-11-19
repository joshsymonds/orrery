// The stories of NeedEntry: a question that opens its epic, a park that offers Retry, a release need that offers Retry
// and Stop…, and a need that is only its sentence.

import type { VNode } from "preact";

import type { EpicCommands } from "./epicpage";
import * as needentry from "./needentry";
import type { Need } from "./needs";
import type { Stories } from "./catalog/story";

const ignore = () => {};

const commands: EpicCommands = {
  pause: ignore,
  resume: ignore,
  stop: ignore,
  retry: ignore,
  answer: () => Promise.resolve(""),
};

// entry draws a need as NeedEntry does for the rota-export epic, with the story's commands and a no-op open.
function entry(need: Need): VNode {
  return <needentry.NeedEntry need={need} slug="rota-export" commands={commands} onOpen={ignore} />;
}

const epic = "lighthouse/rota-export";

export const NeedEntry: Stories = {
  question: () =>
    entry({
      kind: "question",
      text: "rota-export asks: Bump the embedded tz database?",
      summary: "",
      epic,
      question: "Q1",
    }),
  park: () =>
    entry({
      kind: "park",
      text: "rota-export is parked: waiting for the harbour office",
      summary: "",
      epic,
      question: "",
    }),
  release: () =>
    entry({
      kind: "release",
      text: "rota-export: release step 2 (Deploy lighthouse to halyard) failed: halyard refused the build",
      summary: "",
      epic,
      question: "",
    }),
  other: () =>
    entry({
      kind: "other",
      text: "rota-export: no contract is accepted",
      summary: "",
      epic,
      question: "",
    }),
};
