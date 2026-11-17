import type { JSX } from "preact";

// CommitMark draws a commit as a ⎇ mark. Its hover text and accessible label are the full commit id, and the id is
// drawn nowhere as text, so a row shows the mark where the id used to be.
export function CommitMark(props: { hash: string }): JSX.Element {
  const { hash } = props;
  return (
    <span class="commit-mark" role="img" title={hash} aria-label={hash}>
      ⎇
    </span>
  );
}
