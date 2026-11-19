// The stories of the picker: NewPanel before any ask. The fixture refuses every Picker method, so no state asks or runs:
// the panel is drawn before its first ask.

import type { Stories } from "./catalog/story";
import { headless, sessions } from "./fixtures/sessions";
import { panes } from "./fixtures/panes";
import { createFixtureTransport } from "./fixtures/transport";
import { NewPanel as Panel } from "./picker";
import { rowsFrom } from "./rows";

const noop = () => {};

// rows are the rows the app lists for the fixture's panes and sessions, as loadRows makes them.
const rows = rowsFrom(panes.panes, sessions.sessions, headless.sessions);

export const NewPanel: Stories = {
  "before-ask": () => (
    <Panel
      clients={createFixtureTransport().clients}
      rows={rows}
      sheet={false}
      onRun={noop}
      onStart={noop}
      onClose={noop}
    />
  ),
};
