// The stories of SubjectBand: the band's line for a thing open in the panel on the right, which says so and offers no way
// in.

import type { Stories } from "./catalog/story";
import { BandLine as Line } from "./bandline";

const noop = () => {};

export const SubjectBand: Stories = {
  "open-on-right": () => (
    <Line name="tide-alerts" asks="waits on which alert rule wins" act="Answer" open onOpen={noop} />
  ),
};
