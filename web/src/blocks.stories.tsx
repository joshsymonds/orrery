// The stories of the blocks: round 6's YouBlock states, Default and Sending, and its EventBlock. Block's stories are the
// YouBlock's, which Block draws (catalog/coverage.ts storyRenames).

import { EventBlock as EventBlockShape, YouBlock as YouBlockShape } from "./blocks";
import type { Stories } from "./catalog/story";
import { at } from "./fixtures/clock";

export const YouBlock: Stories = {
  default: () => <YouBlockShape text="The uploader loses readings whenever the collector is unreachable. Make it retry." time={at("@14:02")} />,
  sending: () => <YouBlockShape text="Keep the pin and skip the Lisbon case." sending />,
};

export const EventBlock: Stories = {
  default: () => <EventBlockShape text="The planner outlined 7 tasks for **rota-export**. Two start now; five wait on them." time={at("@13:58")} />,
};
