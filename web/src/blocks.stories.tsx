// The stories of the blocks: round 6's YouBlock states, Default and Sending, its EventBlock, and its CheckBlock states, Bad,
// Ok and NoteOpen. Block's stories are the YouBlock's, which Block draws (catalog/coverage.ts storyRenames). Every state
// draws inside the reader's element, where the reader's CSS applies.

import { create } from "@bufbuild/protobuf";

import { CheckBlock as CheckBlockShape, EventBlock as EventBlockShape, YouBlock as YouBlockShape } from "./blocks";
import { type Stories, inReader } from "./catalog/story";
import { at } from "./fixtures/clock";
import { type EpicStepCheck, EpicStepCheckSchema } from "./gen/orrery/v1/epics_pb";

// checkOf is a check a step holds, of the round it ran in.
function checkOf(round: number, name: string, ok: boolean): EpicStepCheck {
  return create(EpicStepCheckSchema, { round, name, ok });
}

const ownedFiles = "owned files: the change touches go.mod, which this task does not own.\nRevert go.mod, or ask the planner to widen the task.";

export const YouBlock: Stories = inReader({
  default: () => <YouBlockShape text="The uploader loses readings whenever the collector is unreachable. Make it retry." time={at("@14:02")} />,
  sending: () => <YouBlockShape text="Keep the pin and skip the Lisbon case." sending />,
});

export const EventBlock: Stories = inReader({
  default: () => <EventBlockShape text="The planner outlined 7 tasks for **rota-export**. Two start now; five wait on them." time={at("@13:58")} />,
});

export const CheckBlock: Stories = inReader({
  bad: () => (
    <CheckBlockShape
      round={2}
      checks={[checkOf(2, "tests", true), checkOf(2, "lint", true), checkOf(2, "owned files", false)]}
      text={ownedFiles}
      time={at("@14:14")}
    />
  ),
  ok: () => (
    <CheckBlockShape
      round={3}
      checks={[checkOf(3, "tests", true), checkOf(3, "lint", true), checkOf(3, "owned files", true)]}
      text="All three checks pass."
      time={at("@14:19")}
    />
  ),
  "note-open": () => (
    <CheckBlockShape
      round={2}
      checks={[checkOf(2, "tests", true), checkOf(2, "lint", true), checkOf(2, "owned files", false)]}
      text={ownedFiles}
      time={at("@14:14")}
      open
    />
  ),
});
