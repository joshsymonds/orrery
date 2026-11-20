// The stories of the blocker pieces: AskFirst, as the rota export epic's Stop asks first. An epic's question and park draw
// as the Ask, whose stories are in ask.stories.tsx, and on the epic page in app.stories.tsx's Page.

import type { Stories } from "./catalog/story";
import * as blocker from "./blocker";

const noop = () => {};

export const ConfirmButton: Stories = {
  rest: () => (
    <blocker.AskFirst
      label="Stop…"
      question="Stop rota-export?"
      confirm="Stop rota-export"
      keep="Keep it running"
      onConfirm={noop}
    />
  ),
};
