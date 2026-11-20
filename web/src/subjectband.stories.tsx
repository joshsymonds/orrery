// The stories of SubjectBand: a conversation's goal alone, its goal with the question its steered epic asks, that epic
// open in the panel, whose line says it is open on the right, and its goal with a contract that waits on the owner.

import type { Stories } from "./catalog/story";
import { SubjectBand as Band } from "./subjectband";

const ignore = () => {};

export const SubjectBand: Stories = {
  goal: () => (
    <Band goal="Every port’s tide table is cached through December 17 and the warm-up is one command." lines={[]} onOpen={ignore} />
  ),
  asks: () => (
    <Band
      goal="Every port’s tide table is cached through December 17 and the warm-up is one command."
      lines={[{ epic: "rotaplan", slug: "rota-export", text: "asks: Bump the embedded tz database?", way: "Answer" }]}
      onOpen={ignore}
    />
  ),
  "open-on-right": () => (
    <Band
      goal="Every port’s tide table is cached through December 17 and the warm-up is one command."
      lines={[{ epic: "rotaplan", slug: "rota-export", text: "asks: Bump the embedded tz database?", way: "Answer" }]}
      onOpen={ignore}
      here="rotaplan"
    />
  ),
  contract: () => (
    <Band
      goal="Every port’s tide table is cached through December 17 and the warm-up is one command."
      lines={[]}
      contracts={[{ id: "tidepool/tide-alerts", slug: "tide-alerts", text: "1 decision is yours; read “can’t be undone” before Accept" }]}
      onOpen={ignore}
      onOpenContract={ignore}
    />
  ),
};
