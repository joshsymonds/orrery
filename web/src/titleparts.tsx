import type { JSX } from "preact";

import type { Row } from "./rows";
import { ReplyText } from "./text";

// lastPathElement is the last element of a directory path, which names the directory in a title.
export function lastPathElement(path: string): string {
  const parts = path.split("/").filter((part) => part !== "");
  return parts[parts.length - 1] ?? "";
}

// listedTitle is the directory and line of the listed row whose kind and id match: the row's project and its label. With
// no matching row the directory is empty and the line is the fallback, or the word session when the fallback is empty.
// It never returns an id, so a title names what the session is doing and where, never what it is called.
export function listedTitle(
  rows: Row[],
  kind: "pane" | "session",
  id: string,
  fallback: string,
): { dir: string; line: string } {
  const listed = rows.find((row) => row.kind === kind && row.id === id);
  if (listed !== undefined) {
    return { dir: listed.project, line: listed.label };
  }
  return { dir: "", line: fallback !== "" ? fallback : "session" };
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
