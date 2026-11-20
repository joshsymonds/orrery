// The stories of Sheet: a sheet titled like a picker, over a few plain rows.

import type { Stories } from "./catalog/story";
import { Sheet as Panel } from "./sheet";

export const Sheet: Stories = {
  menu: () => (
    <Panel title="Pick a session" sub="3 sessions" onClose={() => undefined}>
      <button class="pick-row">orrery · the right rail</button>
      <button class="pick-row">widgets · the nightly build</button>
      <button class="pick-row">notes · the reading list</button>
    </Panel>
  ),
};
