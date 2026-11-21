import type { JSX } from "preact";

import { Markdown } from "./markdown";

// Reply draws the session's answer: its text as plain full-width Markdown inside its reply element, with nothing around
// it. live is whether the text is still being written, so its caret follows the Markdown. query and current mark the
// find's matches in the text, as Markdown marks them.
export function Reply({
  text,
  live = false,
  query = "",
  current = -1,
}: {
  text: string;
  live?: boolean;
  query?: string;
  current?: number;
}): JSX.Element {
  return (
    <div class="reply" data-shape="reply">
      <Markdown text={text} query={query} current={current} />
      {live && <span class="caret soft" />}
    </div>
  );
}
