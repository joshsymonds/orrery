// The story of Mark: the orrery mark, drawn inline as the wireframe's #mark symbol.

import * as mark from "./mark";
import type { Stories } from "./catalog/story";

export const Mark: Stories = {
  "orrery-mark": () => <mark.Mark />,
};
