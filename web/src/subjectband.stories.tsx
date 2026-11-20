// The stories of SubjectBand: a conversation's goal alone, its goal with the question its steered epic asks, and that
// epic open in the panel, whose line says it is open on the right.

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
};
