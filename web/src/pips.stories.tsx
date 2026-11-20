// The stories of Pips: a task's five cells drawn as its five marks, in each state round 6 draws the pips in.

import * as pips from "./pips";
import type { Stories } from "./catalog/story";

export const Pips: Stories = {
  accepted: () => <pips.Pips cells={["ok", "ok", "ok", "ok", "acc"]} />,
  done: () => <pips.Pips cells={["ok", "ok", "ok", "ok", "ok"]} />,
  failed: () => <pips.Pips cells={["ok", "bad", "none", "none", "none"]} />,
  held: () => <pips.Pips cells={["ok", "mine", "none", "none", "none"]} />,
  limited: () => <pips.Pips cells={["ok", "limited", "none", "none", "none"]} />,
  "not-started": () => <pips.Pips cells={["none", "none", "none", "none", "none"]} />,
  running: () => <pips.Pips cells={["ok", "run", "none", "none", "none"]} />,
};
