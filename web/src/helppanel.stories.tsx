// The stories of HelpPanel: the keys section App shows while help is open.

import type { Stories } from "./catalog/story";
import { HelpPanel as Panel } from "./helppanel";

export const HelpPanel: Stories = {
  open: () => <Panel />,
};
