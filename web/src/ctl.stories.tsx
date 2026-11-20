// The stories of Ctl: the five round-6 states, Interrupt with esc and a bad tone, Resume with a go tone, Send now, the icon
// alone as More, and Disabled while an interrupt is being sent. The controls take no clients, so their clicks do nothing.

import type { Stories } from "./catalog/story";
import { Ctl as Control } from "./ctl";

const noop = () => {};

export const Ctl: Stories = {
  interrupt: () => <Control label="Interrupt" icon="stop" tone="bad" kbd="esc" onClick={noop} />,
  resume: () => <Control label="Resume" icon="play" tone="go" onClick={noop} />,
  "send-now": () => <Control label="Send now" icon="send" onClick={noop} />,
  "icon-only": () => <Control label="More" icon="more" only onClick={noop} />,
  disabled: () => <Control label="Interrupting…" disabled onClick={noop} />,
};
