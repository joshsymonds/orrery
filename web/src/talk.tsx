import type { JSX } from "preact";
import { useRef, useState } from "preact/hooks";

import { clock } from "./reader";

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

// TalkTarget is what Talk opens: a pane, or a session by its id, with the label that names it.
export interface TalkTarget {
  kind: "pane" | "session";
  id: string;
  label: string;
}

// shortIdLength is how many characters of a session's id name it when it has no label.
const shortIdLength = 8;

// talkTarget is the target a session opens: its pane when it has one, else the session. Its label is the session's
// label, else the first characters of its id.
export function talkTarget(session: TalkSession): TalkTarget {
  const label = session.label !== "" ? session.label : session.id.slice(0, shortIdLength);
  return session.pane !== ""
    ? { kind: "pane", id: session.pane, label }
    : { kind: "session", id: session.id, label };
}

// talkLine is a session's line in Talk: its label, its newest call and the clock of that call.
export function talkLine(session: TalkSession): string {
  return `${talkTarget(session).label} · ${session.call} · ${clock(session.at)}`;
}

// lastPathElement is the last element of a path, which names the repository in a new session's line.
function lastPathElement(path: string): string {
  const parts = path.split("/").filter((part) => part !== "");
  return parts[parts.length - 1] ?? "";
}

// Talk is the epic's Talk command: a button that opens and closes the list of the sessions that touched the epic. A
// session's line opens its target and closes the list. When the epic has a repository, the list's last line starts a
// session there with talkPrompt, disabled while the start runs, and opens the session it returns. A refused start shows
// its message and opens nothing. Every text is a child of its element.
export function Talk(props: {
  epic: string;
  sessions: TalkSession[];
  repository: string;
  start: (dir: string, prompt: string) => Promise<string>;
  onOpen: (target: TalkTarget) => void;
}): JSX.Element {
  const { epic, sessions, repository, start, onOpen } = props;
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
          {sessions.map((session) => (
            <li key={session.id}>
              <button
                type="button"
                onClick={() => {
                  setOpen(false);
                  onOpen(talkTarget(session));
                }}
              >
                {talkLine(session)}
              </button>
            </li>
          ))}
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
