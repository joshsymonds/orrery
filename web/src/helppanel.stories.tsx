// The stories of KeyList: the keys panel App shows while help is open.

import type { Stories } from "./catalog/story";
import { KeyList as Panel } from "./helppanel";

export const KeyList: Stories = {
  open: () => <Panel onClose={() => undefined} />,
};
