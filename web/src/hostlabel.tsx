// HostLabel: the host a pane, a session, an epic or a step runs on, drawn with the host's icon. Inline it sits on its own
// row; on a card's heading it is drawn once, for the rows under it. Down, the word unreachable sits beside it.

import type { JSX } from "preact";

import { Icon } from "./glyphs";

// HostLabel draws a host by its name. An empty name draws nothing.
export function HostLabel({
  name,
  heading = false,
  down = false,
}: {
  name: string;
  heading?: boolean;
  down?: boolean;
}): JSX.Element | null {
  if (name === "") {
    return null;
  }
  return (
    <>
      <span class={heading ? "host-label heading" : "host-label"}>
        <Icon name="host" />
        <span class="name">{name}</span>
      </span>
      {down ? <span class="unreach">unreachable</span> : null}
    </>
  );
}
