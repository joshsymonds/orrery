// The stories of the picker: PickerPalette before any ask, and PickerStartForm for a folder row, empty and for a prompt
// row with the asked words. The fixture refuses every Picker method, so no state asks or runs: the palette is drawn
// before its first ask, and each start form before its Start is sent.

import { create } from "@bufbuild/protobuf";

import type { Stories } from "./catalog/story";
import { headless, sessions } from "./fixtures/sessions";
import { panes } from "./fixtures/panes";
import { createFixtureTransport } from "./fixtures/transport";
import { PickerRowSchema } from "./gen/orrery/v1/orrery_pb";
import { PickerPalette as Palette, PickerStartForm as StartForm } from "./picker";
import { rowsFrom } from "./rows";

const noop = () => {};

// rows are the rows the app lists for the fixture's panes and sessions, as loadRows makes them.
const rows = rowsFrom(panes.panes, sessions.sessions, headless.sessions);

// A folder row that opens a start form with an empty message, and a prompt row whose form starts with the asked words.
const folder = create(PickerRowSchema, {
  token: "tok-fresh-orchard",
  kind: "fresh",
  dir: "/home/owner/code/orchard",
  label: "",
  note: "start a session in this folder",
  prompt: false,
});
const promptRow = create(PickerRowSchema, {
  token: "tok-pane-lighthouse",
  kind: "pane",
  dir: "/home/owner/code/lighthouse",
  pane: "%14",
  label: "",
  note: "ask this pane",
  prompt: true,
});

export const PickerPalette: Stories = {
  "before-ask": () => (
    <Palette
      clients={createFixtureTransport().clients}
      rows={rows}
      onRun={noop}
      onStart={noop}
      onClose={noop}
    />
  ),
};

export const PickerStartForm: Stories = {
  folder: () => (
    <StartForm clients={createFixtureTransport().clients} folder={folder} asked="" onStarted={noop} />
  ),
  "prompt-row": () => (
    <StartForm
      clients={createFixtureTransport().clients}
      folder={promptRow}
      asked="migrate the rota table to ISO weeks"
      onStarted={noop}
    />
  ),
};
