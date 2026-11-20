import type { ComponentChildren, JSX } from "preact";

import { Icon } from "./glyphs";
import type { Project } from "./tree";

// DirProps are a project's card: the project; whether it is folded; the waiting count a folded head says; whether the
// ended line is open; the presses; the conversations that hang under the head, which the caller draws; the ended
// conversations, which the caller draws and the card shows only while the ended line is open; and, when given, the press
// that opens the project's page from its head.
export interface DirProps {
  project: Project;
  folded: boolean;
  waiting: number;
  endedOpen: boolean;
  onFold: () => void;
  onEnded: () => void;
  onOpen?: (key: string) => void;
  children: ComponentChildren;
  ended: ComponentChildren;
}

// Dir draws a project's card. Its head is the fold button, the folder icon, the project's name and path, and a host chip
// only when the project is on another host. With onOpen, the folder, the name and the path are one button that opens the
// project. Folded, the head also says how many things wait on you (only when the count is not zero) and how many
// conversations it holds, and nothing else is drawn. Open, the conversations hang from a line under the head, then the
// ended line, which opens to the ended conversations.
export function Dir({
  project,
  folded,
  waiting,
  endedOpen,
  onFold,
  onEnded,
  onOpen,
  children,
  ended,
}: DirProps): JSX.Element {
  const label = (
    <>
      <Icon name="folder" />
      <span class="nm">{project.name}</span>
      <span class="path">{project.path}</span>
    </>
  );
  const head = (
    <div class="dir-head">
      <button
        type="button"
        class="fold"
        aria-expanded={!folded}
        aria-label={`${folded ? "Unfold" : "Fold"} ${project.name}`}
        onClick={() => onFold()}
      >
        <Icon name={folded ? "chevronRight" : "chevronDown"} />
      </button>
      {onOpen === undefined ? (
        <div class="proj">{label}</div>
      ) : (
        <button
          type="button"
          class="proj"
          title={`Everything working in ${project.path === "" ? project.name : project.path}`}
          onClick={() => onOpen(project.key)}
        >
          {label}
        </button>
      )}
      <span class="end">
        {project.host !== "" && (
          <span class="chip">
            <Icon name="host" />
            {project.host}
          </span>
        )}
        {folded && waiting > 0 && <span class="is-attention">{waiting} waiting</span>}
        {folded && <span>{conversationCount(project)}</span>}
      </span>
    </div>
  );
  if (folded) {
    return (
      <section class="dir" aria-label={project.name}>
        {head}
      </section>
    );
  }
  const hasEnded = project.ended.length > 0;
  return (
    <section class="dir" aria-label={project.name}>
      {head}
      <div class="dir-kids">
        {children}
        {hasEnded && (
          <button type="button" class="fold-line" aria-expanded={endedOpen} onClick={() => onEnded()}>
            <Icon name={endedOpen ? "chevronDown" : "chevronRight"} />
            <span>{project.ended.length} ended</span>
          </button>
        )}
        {hasEnded && endedOpen && ended}
      </div>
    </section>
  );
}

// conversationCount is how many conversations a project holds: each row once, however many times it is drawn, and no
// stand-in, which is what steers with no conversation of its own.
function conversationCount(project: Project): number {
  const keys = new Set<string>();
  for (const convo of [...project.convos, ...project.ended]) {
    if (convo.row !== null) {
      keys.add(convo.row.key);
    }
  }
  return keys.size;
}
