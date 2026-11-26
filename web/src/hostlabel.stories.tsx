// The stories of HostLabel: a host inline on its own row, as a card's heading, and beside the word unreachable.

import type { Stories } from "./catalog/story";
import * as hostlabel from "./hostlabel";

export const HostLabel: Stories = {
  inline: () => <hostlabel.HostLabel name="halyard" />,
  heading: () => <hostlabel.HostLabel name="halyard" heading />,
  down: () => <hostlabel.HostLabel name="brine" down />,
};
