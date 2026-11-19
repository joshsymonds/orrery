// The stories of Dock, drawn by StepBox: a held step, whose edge and placeholder say it waits for a nudge to resume it, and
// a running step, whose placeholder says a nudge interrupts its turn. The box takes no clients, so its calls resolve at
// once.

import type { Stories } from "./catalog/story";
import * as stepbox from "./stepbox";

const landed = () => Promise.resolve();

export const Dock: Stories = {
  "step-held": () => <stepbox.StepBox label="T1" held nudge={landed} hold={landed} stop={landed} />,
  "step-running": () => <stepbox.StepBox label="T2" held={false} nudge={landed} hold={landed} stop={landed} />,
};
