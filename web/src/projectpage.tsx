import type { JSX } from "preact";
import { useState } from "preact/hooks";

import { hungName } from "./epicrow";
import { Icon, StateGlyph } from "./glyphs";
import { ageText, clockText, parseTool, quietRow, shortName, stateWaiting, waitingText } from "./inboxrow";
import type { Row } from "./rows";
import { TitleRow } from "./titlerow";
import { type Convo, changedOf, type Hung, type Project } from "./tree";
import { projectWaiting } from "./treefilter";
import { WorkCard, workCardOf } from "./workcard";

// ProjectPage draws one project of the inbox tree as its page: its name in the title row with New session here (in the
// foot bar under 760 px, with Back in the title row), its path and host, its counts, its Conversations with what each
// last said or asks and its WorkCards, and the ended line, which unfolds the ended conversations. A conversation and a
// WorkCard each call onOpen with their key. The counts and the last change are the tree's own, read from the project.
export function ProjectPage({
  project,
  now,
  narrow,
  onOpen,
  onNewHere,
  onBack,
}: {
  project: Project;
  now: number;
  narrow: boolean;
  onOpen: (key: string) => void;
  onNewHere: (dir: string) => void;
  onBack: () => void;
}): JSX.Element {
  const [unfolded, setUnfolded] = useState(false);
  const epics = project.convos.reduce((total, convo) => total + convo.hung.filter((hung) => hung.kind === "epic").length, 0);
  const waits = projectWaiting(project);
  const newest = Math.max(0, ...[...project.convos, ...project.ended].map((convo) => changedOf(convo)));
  const newHere = project.dir !== "";
  return (
    <section class="read project" aria-label="project">
      <div class="head">
        <TitleRow crumbs={[{ label: project.name }]} onBack={onBack}>
          {!narrow && newHere && <NewHereButton grow={false} onClick={() => onNewHere(project.dir)} />}
        </TitleRow>
      </div>
      <div class="spage">
        <div class="inner">
          <section class="proj-head">
            <span class="path">
              {project.path}
              {project.host !== "" && ` · on ${project.host}`}
            </span>
            <div class="counts">
              <span>
                <Icon name="chat" />
                {countWords(project.convos.length, "conversation", "conversations")}
              </span>
              <span>
                <Icon name="pools" />
                {countWords(epics, "epic", "epics")}
              </span>
              {waits > 0 && (
                <span class="is-attention">
                  <Icon name="question" />
                  {waitsWords(waits)}
                </span>
              )}
              {newest > 0 && (
                <span>
                  <Icon name="clock" />
                  {`last change ${ageText(newest, now)} ago`}
                </span>
              )}
            </div>
          </section>
          <section>
            <div class="sec">
              <span class="name">Conversations</span>
              <span class="n">{project.convos.length}</span>
            </div>
            <div class="pconvs">
              {project.convos.map((convo) => (
                <ConvoLine key={convo.key} convo={convo} now={now} onOpen={onOpen} />
              ))}
            </div>
          </section>
          {project.ended.length > 0 && (
            <section>
              <button type="button" class="fold-line" aria-expanded={unfolded} onClick={() => setUnfolded((open) => !open)}>
                <Icon name={unfolded ? "chevronDown" : "chevronRight"} />
                <span>{countWords(project.ended.length, "ended conversation", "ended conversations")}</span>
              </button>
              {unfolded && (
                <div class="pconvs">
                  {project.ended.map((convo) => (
                    <ConvoLine key={convo.key} convo={convo} now={now} onOpen={onOpen} />
                  ))}
                </div>
              )}
            </section>
          )}
        </div>
      </div>
      {narrow && newHere && (
        <div class="cmdbar">
          <NewHereButton grow onClick={() => onNewHere(project.dir)} />
        </div>
      )}
    </section>
  );
}

// NewHereButton is the New session here command, which starts a session in the project's directory.
function NewHereButton({ grow, onClick }: { grow: boolean; onClick: () => void }): JSX.Element {
  return (
    <button type="button" class={grow ? "btn grow" : "btn"} onClick={onClick}>
      <Icon name="plus" />
      New session here
    </button>
  );
}

// ConvoLine draws one conversation of the page: a button that opens it, with its state's glyph, its title, what it last
// said or asks, and its age, and under it a WorkCard for each thing the tree hung under it. A conversation with no row of
// its own is its one thing: its title is that thing's name, its glyph is the state its card reads, and it says nothing it
// last said. A quiet row, waiting but put off or archived, draws the idle glyph, as the inbox row does.
function ConvoLine({ convo, now, onOpen }: { convo: Convo; now: number; onOpen: (key: string) => void }): JSX.Element {
  const row = convo.row;
  const [thing] = convo.hung;
  return (
    <div class="pconv">
      <button type="button" class="pc-row" data-key={convo.key} onClick={() => onOpen(convo.key)}>
        <StateGlyph state={row === null ? ownState(thing, now) : quietRow(row) ? "idle" : row.state} />
        <span class="t">{row !== null ? row.label : hungName(thing)}</span>
        {row !== null && <Said row={row} now={now} />}
        <span class="end">
          <span class="id">{ageText(changedOf(convo), now)}</span>
          <Icon name="chevronRight" />
        </span>
      </button>
      {convo.hung.length > 0 && (
        <div class="pc-kids">
          <div class="cards">
            {convo.hung.map((hung) => (
              <WorkCard key={hung.key} card={workCardOf(hung, now)} onOpen={onOpen} />
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

// ownState is the glyph state of a thing drawn as its own conversation: waiting on you, working, or idle, as its card's
// tone reads it.
function ownState(hung: Hung, now: number): string {
  const tone = workCardOf(hung, now).tone;
  if (tone === "attention") {
    return "waiting";
  }
  return tone === "live" ? "working" : "idle";
}

// Said draws what a row last said or asks, in the inbox row's words: a waiting row's question, led by its icon, or else
// what it waits on, in the asks style unless the row is quiet; a working pane's tool, its argument and its clock; a
// terminal with no conversation; a session's summary. Any other row says nothing.
function Said({ row, now }: { row: Row; now: number }): JSX.Element | null {
  if (row.state === stateWaiting) {
    const asks = row.question !== "";
    return (
      <span class={asks && !quietRow(row) ? "said asked" : "said"}>
        {asks && <Icon name="question" />}
        {waitingText(row)}
      </span>
    );
  }
  const tool = row.kind === "pane" && row.state === "working" ? parseTool(row.tool) : null;
  if (tool !== null) {
    return (
      <span class="said">
        <span class="name">{shortName(tool.name)}</span>
        {tool.input !== "" && ` ${tool.input}`}
        {tool.start > 0 && ` · ${clockText(now - tool.start)}`}
      </span>
    );
  }
  if (row.kind === "pane" && row.state === "") {
    return <span class="said">a terminal with no conversation</span>;
  }
  if (row.kind === "session" && row.summary !== "") {
    return <span class="said">{row.summary}</span>;
  }
  return null;
}

// countWords is a count with its noun, singular for one.
function countWords(count: number, one: string, many: string): string {
  return `${count} ${count === 1 ? one : many}`;
}

// waitsWords is what waits on you, counted.
function waitsWords(count: number): string {
  return count === 1 ? "1 thing waits on you" : `${count} things wait on you`;
}
