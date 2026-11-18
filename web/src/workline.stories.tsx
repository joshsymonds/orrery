// The stories of Pill: a working pane with its tool and clock, a working row with neither, an interrupted working row, a
// waiting and a done pane, a failed session, and a step that runs or is held. Each pill's model is the one row and step
// pills are given, from the fixture panes.

import type { Pane } from "./gen/orrery/v1/orrery_pb";
import { at, liveEpoch } from "./fixtures/clock";
import { panes } from "./fixtures/panes";
import * as workline from "./workline";
import type { Stories } from "./catalog/story";

// now is the instant the fixtures are set at, in unix seconds, as the pills' clocks read it.
const now = Date.parse(liveEpoch) / 1000;

// paneWith is the fixture pane with the id, which every pill of a pane is drawn from.
function paneWith(id: string): Pane {
  const pane = panes.panes.find((candidate) => candidate.id === id);
  if (pane === undefined) {
    throw new Error(`fixtures: no pane ${id}`);
  }
  return pane;
}

export const Pill: Stories = {
  "working-tool": () => <workline.Pill model={workline.rowPill("working", paneWith("%11"))} now={now} />,
  "working-no-tool": () => <workline.Pill model={workline.rowPill("working")} now={now} />,
  "interrupting": () => <workline.Pill model={workline.rowPill("working", paneWith("%11"), true)} now={now} />,
  "waiting": () => <workline.Pill model={workline.rowPill("waiting", paneWith("%12"))} now={now} />,
  "done": () => <workline.Pill model={workline.rowPill("done", paneWith("%13"))} now={now} />,
  "failed": () => <workline.Pill model={workline.rowPill("failed")} now={now} />,
  "step-running": () => <workline.Pill model={workline.stepPill(false, Number(at("@14:40:30")))} now={now} />,
  "step-held": () => <workline.Pill model={workline.stepPill(true, 0)} now={now} />,
};
