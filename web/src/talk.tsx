import type { JSX } from "preact";
import { useRef, useState } from "preact/hooks";

import { clock } from "./reader";
import type { Row } from "./rows";
import { lastPathElement, listedTitle, TitleParts } from "./titleparts";

// talkPrompt is the first prompt of a session that Talk starts: it names the epic and has the session read it.
export function talkPrompt(epic: string): string {
  return `This session is about orrery epic ${epic}. Read it with epic_get, then wait for my words.`;
}

// TalkSession is a session that touched the epic: its id, the pane it runs in, its label, and its newest call and time.
export interface TalkSession {
  id: string;
  pane: string;
  label: string;
  call: string;
  at: bigint;
}

// TalkTarget is what Talk opens: a pane, or a session by its id, with the served label that names it when no listed row
// does.
export interface TalkTarget {
  kind: "pane" | "session";
  id: string;
  label: string;
}

// talkTarget is the target a session opens: its pane when it has one, else the session. Its label is the session's
// served label alone.
export function talkTarget(session: TalkSession): TalkTarget {
  return session.pane !== ""
    ? { kind: "pane", id: session.pane, label: session.label }
    : { kind: "session", id: session.id, label: session.label };
}

// Talk is the epic's Talk command: a button that opens and closes the list of the sessions that touched the epic. A
// session's line draws its listed row's directory and line through TitleParts, then its newest call and the clock of that
// call, and opens its target and closes the list. When the epic has a repository, the list's last line starts a session
// there with talkPrompt, disabled while the start runs, and opens the session it returns. A refused start shows its
// message and opens nothing. Every text is a child of its element.
export function Talk(props: {
  epic: string;
  sessions: TalkSession[];
  repository: string;
  rows: Row[];
  start: (dir: string, prompt: string) => Promise<string>;
  onOpen: (target: TalkTarget) => void;
}): JSX.Element {
  const { epic, sessions, repository, rows, start, onOpen } = props;
  const [open, setOpen] = useState(false);
  const [starting, setStarting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // inFlight is set while a start is pending, so a press that lands before the disabled state renders starts nothing.
  const inFlight = useRef(false);
  const repositoryName = lastPathElement(repository);

  async function startSession(): Promise<void> {
    if (inFlight.current) {
      return;
    }
    inFlight.current = true;
    setStarting(true);
    setError(null);
    let id: string;
    try {
      id = await start(repository, talkPrompt(epic));
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : String(failure));
      return;
    } finally {
      inFlight.current = false;
      setStarting(false);
    }
    setOpen(false);
    onOpen({ kind: "session", id, label: repositoryName });
  }

  return (
    <>
      <button type="button" class="act" aria-expanded={open} onClick={() => setOpen((isOpen) => !isOpen)}>
        Talk
      </button>
      {open && (
        <ul class="talk">
          {sessions.map((session) => {
            const title = listedTitle(
              rows,
              session.pane !== "" ? "pane" : "session",
              session.pane || session.id,
              session.label,
            );
            return (
              <li key={session.id}>
                <button
                  type="button"
                  onClick={() => {
                    setOpen(false);
                    onOpen(talkTarget(session));
                  }}
                >
                  <TitleParts dir={title.dir} line={title.line} />
                  {` · ${session.call} · ${clock(session.at)}`}
                </button>
              </li>
            );
          })}
          {repository !== "" && (
            <li>
              <button type="button" disabled={starting} onClick={() => void startSession()}>
                {`New session in ${repositoryName}`}
              </button>
              {error !== null && (
                <p class="error" role="alert">
                  {error}
                </p>
              )}
            </li>
          )}
        </ul>
      )}
    </>
  );
}
