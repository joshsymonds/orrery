import type { JSX } from "preact";

import { ReplyText } from "./text";

// lastPathElement is the last element of a directory path, which names the directory in a title.
export function lastPathElement(path: string): string {
  const parts = path.split("/").filter((part) => part !== "");
  return parts[parts.length - 1] ?? "";
}

// TitleParts draws a session's or pane's title: the last element of its directory in mauve, then its line when the line
// is not empty. The line is reply text, so a link in it stays a link. It draws no id and no hash, so a title names what
// the session is doing and where.
export function TitleParts({ dir, line }: { dir: string; line: string }): JSX.Element {
  const name = lastPathElement(dir);
  return (
    <>
      {name !== "" && <span class="title-dir">{name}</span>}
      {name !== "" && line !== "" && " "}
      {line !== "" && (
        <span class="title-line">
          <ReplyText text={line} />
        </span>
      )}
    </>
  );
}
