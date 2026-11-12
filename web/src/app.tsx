import { useEffect, useRef, useState } from "preact/hooks";

import * as actions from "./actions";
import type { Clients } from "./actions";
import { Composer } from "./composer";
import type { Conversation } from "./conversation";
import { follow } from "./follow";
import { Reader } from "./reader";
import { byAttention, byProject, type Row, type Section } from "./rows";
import { type SeenClock, type SeenWindow, watchSeen } from "./seen";
import { ReplyText } from "./text";

type View = "attention" | "project";

// The conversation the reader holds, with the row key it belongs to, so a row never shows another row's transcript.
interface Stream {
  key: string;
  conversation: Conversation;
}

// The cursor is held by row key, so it survives new rows. index is where it sat, used when its row leaves.
interface Cursor {
  key: string;
  index: number;
}

const stateWaiting = "waiting";
const noDirectoryTitle = "no directory";
const seenFailedText = "could not mark seen";

const secondsPerMinute = 60;
const minutesPerHour = 60;
const hoursPerDay = 24;

const helpEntries: [string[], string][] = [
  [["j", "k", "↓", "↑"], "move the cursor one row up or down"],
  [["gg"], "first row"],
  [["G"], "last row"],
  [["a"], "by attention"],
  [["p"], "by project"],
  [["w"], "first waiting row"],
  [["n"], "next unseen row, wrapping"],
  [["x", "Enter"], "mark the cursor's row seen"],
  [["e"], "mark it seen, then go to the next unseen row"],
  [["/"], "filter: Enter leaves it, Esc clears it"],
  [["i"], "focus the reply box"],
  [["o"], "open or close a compaction's summary"],
  [["?"], "toggle this help"],
  [["Esc"], "close this help"],
];

export function App({
  rows,
  error,
  now,
  clients,
  seenClock,
  seenWindow,
}: {
  rows: Row[];
  error: string;
  now: number;
  clients: Clients;
  seenClock?: SeenClock;
  seenWindow?: SeenWindow;
}) {
  const [view, setView] = useState<View>("attention");
  const [cursor, setCursor] = useState<Cursor>({ key: "", index: 0 });
  const [filter, setFilter] = useState("");
  const [helpOpen, setHelpOpen] = useState(false);
  const [open, setOpen] = useState(false);
  const [stream, setStream] = useState<Stream | null>(null);
  // The rows the cursor has rested on, each with its own composer, so a draft waits where it was typed. A row stays
  // here while it is missing from the list, as it is for a poll whose list call failed, so its draft is not lost.
  const [visited, setVisited] = useState<Row[]>([]);
  const inputRef = useRef<HTMLInputElement>(null);
  const composerInputs = useRef(new Map<string, HTMLTextAreaElement>());
  const pendingG = useRef(false);
  const handler = useRef<(event: KeyboardEvent) => void>();
  // The changed time each row had when its seen write succeeded. A row reads as seen until its changed time moves past it.
  const [seenWritten, setSeenWritten] = useState<Map<string, number>>(() => new Map());
  const [seenError, setSeenError] = useState("");
  const seenWatch = useRef<ReturnType<typeof watchSeen>>();
  // The row the seen watch last rested on, which its write marks seen.
  const restedRow = useRef<Row | null>(null);

  const words = filter
    .toLowerCase()
    .split(/\s+/)
    .filter((word) => word !== "");
  const shownRows = rows.map((row) => readAsSeen(row, seenWritten));
  const matching = shownRows.filter((row) => matchesFilter(row, words));
  const sections = view === "attention" ? byAttention(matching) : byProject(matching);
  const flat = sections.flatMap((section) => section.rows);
  const index = resolveIndex(flat, cursor);
  const cursorRow = index >= 0 ? flat[index] : null;
  const cursorRowKey = cursorRow?.key ?? "";
  const shownIndex = Math.max(index, 0);

  // The held cursor follows the row shown, so a first render or a clamp does not leave it on a stale key.
  useEffect(() => {
    if (cursor.key !== cursorRowKey || cursor.index !== shownIndex) {
      setCursor({ key: cursorRowKey, index: shownIndex });
    }
  }, [cursor, cursorRowKey, shownIndex]);

  // The cursor's row is followed while the cursor rests on it: a move to another row, or an unmount, stops the stream.
  useEffect(() => {
    if (cursorRow === null) {
      return undefined;
    }
    return follow(clients, { kind: cursorRow.kind, id: cursorRow.id }, (conversation) =>
      setStream({ key: cursorRowKey, conversation }),
    );
  }, [cursorRowKey, clients]);

  useEffect(() => {
    setVisited((previous) => keepVisited(previous, rows, cursorRow));
  }, [rows, cursorRowKey]);

  // writeSeen marks a row seen through actions.markSeen. A success reads the row as seen; a failure leaves it unseen and
  // shows a fixed line.
  const writeSeen = async (row: Row) => {
    try {
      await actions.markSeen(clients, row);
    } catch {
      setSeenError(seenFailedText);
      return;
    }
    setSeenError("");
    setSeenWritten((previous) => new Map(previous).set(row.key, row.changed));
  };

  // The seen watch runs for the page's life: its write marks the row it last rested on, and unmount stops it.
  useEffect(() => {
    const watch = watchSeen(
      seenWindow ?? window,
      () => {
        const row = restedRow.current;
        if (row !== null) {
          void writeSeen(row);
        }
      },
      seenClock,
    );
    seenWatch.current = watch;
    return () => watch.stop();
  }, [clients, seenClock, seenWindow]);

  // Each render tells the watch the cursor's row and whether it reads as unseen. A changed row is a new key, so a
  // change made while the cursor rests on it gets its own dwell.
  useEffect(() => {
    restedRow.current = cursorRow;
    seenWatch.current?.rest(
      cursorRow === null ? "" : `${cursorRow.key}@${cursorRow.changed}`,
      cursorRow?.unseen ?? false,
    );
  });

  const moveTo = (target: number) => {
    if (flat.length === 0) {
      return;
    }
    const next = clamp(target, flat.length);
    setCursor({ key: flat[next].key, index: next });
  };

  const nextUnseen = () => {
    for (let step = 1; step <= flat.length; step++) {
      const target = (index + step) % flat.length;
      if (flat[target].unseen) {
        moveTo(target);
        return;
      }
    }
  };

  const handleKey = (event: KeyboardEvent) => {
    if (event.ctrlKey || event.metaKey || event.altKey) {
      return;
    }
    const input = inputRef.current;
    if (input !== null && document.activeElement === input) {
      if (event.key === "Enter") {
        input.blur();
      } else if (event.key === "Escape") {
        setFilter("");
        input.blur();
      }
      return;
    }
    // Keys typed in the reply box are text: only Escape leaves it.
    const focusedComposer = [...composerInputs.current.values()].find((box) => box === document.activeElement);
    if (focusedComposer !== undefined) {
      if (event.key === "Escape") {
        focusedComposer.blur();
      }
      return;
    }

    const afterG = pendingG.current;
    pendingG.current = false;
    switch (event.key) {
      case "g":
        if (afterG) {
          moveTo(0);
        } else {
          pendingG.current = true;
        }
        break;
      case "j":
      case "ArrowDown":
        if (index >= 0) {
          moveTo(index + 1);
        }
        break;
      case "k":
      case "ArrowUp":
        if (index >= 0) {
          moveTo(index - 1);
        }
        break;
      case "G":
        moveTo(flat.length - 1);
        break;
      case "a":
        setView("attention");
        break;
      case "p":
        setView("project");
        break;
      case "w": {
        const waiting = flat.findIndex((row) => row.state === stateWaiting);
        if (waiting >= 0) {
          moveTo(waiting);
        }
        break;
      }
      case "n":
        nextUnseen();
        break;
      case "x":
      case "Enter":
        if (cursorRow !== null) {
          void writeSeen(cursorRow);
        }
        break;
      case "e":
        if (cursorRow !== null) {
          void writeSeen(cursorRow);
          nextUnseen();
        }
        break;
      case "/":
        inputRef.current?.focus();
        break;
      case "i":
        composerInputs.current.get(cursorRowKey)?.focus();
        break;
      case "o":
        setOpen((value) => !value);
        break;
      case "?":
        setHelpOpen((open) => !open);
        break;
      case "Escape":
        setHelpOpen(false);
        return;
      default:
        return;
    }
    event.preventDefault();
  };
  handler.current = handleKey;

  useEffect(() => {
    const listener = (event: KeyboardEvent) => handler.current?.(event);
    document.addEventListener("keydown", listener);
    return () => document.removeEventListener("keydown", listener);
  }, []);

  return (
    <main class="app">
      <header class="top">
        <div class="brand">orrery</div>
        <div class="filter">
          <kbd>/</kbd>
          <input
            ref={inputRef}
            value={filter}
            placeholder="Filter"
            aria-label="filter rows"
            onInput={(event) => setFilter(event.currentTarget.value)}
          />
        </div>
        <div class="views">
          <span class={view === "attention" ? "view on" : "view"}>
            <kbd>a</kbd>By attention
          </span>
          <span class={view === "project" ? "view on" : "view"}>
            <kbd>p</kbd>By project
          </span>
        </div>
      </header>
      {error !== "" && (
        <p class="error" role="alert">
          {error}
        </p>
      )}
      {seenError !== "" && (
        <p class="error" role="alert">
          {seenError}
        </p>
      )}
      <div class="panes">
        <div class="list">
          <div class="rows" data-list={view}>
            {sections.map((section) => (
              <div class="section">
                <div class="b-sec">{sectionTitle(section)}</div>
                {section.rows.map((item) => (
                  <div
                    key={item.key}
                    class={item.key === cursorRowKey ? "b-row cur" : "b-row"}
                    data-row=""
                    data-key={item.key}
                    data-unseen={item.unseen ? "" : undefined}
                    aria-current={item.key === cursorRowKey ? "true" : undefined}
                  >
                    <span class="dot" />
                    <span class={`g g-${item.state}`} aria-hidden="true" />
                    <div class="main">
                      <div class="l1">
                        <span class={`state st-${item.state}`}>{item.state}</span>
                        <span class="label">{item.label}</span>
                        <span class="proj">{lastPathElement(item.project)}</span>
                        <span class="age">{ageText(item.changed, now)}</span>
                      </div>
                      <div class="l2">
                        <ReplyText text={item.state === stateWaiting && item.question !== "" ? item.question : item.summary} />
                      </div>
                    </div>
                  </div>
                ))}
              </div>
            ))}
          </div>
          {helpOpen && (
            <section class="keys" aria-label="keys">
              <dl>
                {helpEntries.map(([keys, text]) => (
                  <div key={text} class="key">
                    <dt>
                      {keys.map((key) => (
                        <kbd key={key}>{key}</kbd>
                      ))}
                    </dt>
                    <dd>{text}</dd>
                  </div>
                ))}
              </dl>
            </section>
          )}
        </div>
        <section class="read" aria-label="reader" hidden={cursorRow === null}>
          {cursorRow !== null && (
            <Reader
              conversation={stream !== null && stream.key === cursorRowKey ? stream.conversation : null}
              seenAt={cursorRow.seen}
              open={open}
            />
          )}
          {composerRows(visited, cursorRow).map((row) => (
              <div key={row.key} class="reply" hidden={row.key !== cursorRowKey}>
                <Composer
                  label={row.label}
                  send={(text, promptId) => actions.send(clients, row, text, promptId)}
                  pull={row.kind === "pane" ? () => actions.pullQueued(clients, row) : null}
                  newPromptId={actions.newPromptId}
                  inputRef={(element) => {
                    if (element === null) {
                      composerInputs.current.delete(row.key);
                    } else {
                      composerInputs.current.set(row.key, element);
                    }
                  }}
                />
              </div>
            ))}
        </section>
      </div>
    </main>
  );
}

// matchesFilter keeps a row whose label, project, host, state or summary holds every typed word.
function matchesFilter(row: Row, words: string[]): boolean {
  const haystack = [row.label, row.project, row.host, row.state, row.summary].join(" ").toLowerCase();
  return words.every((word) => haystack.includes(word));
}

// readAsSeen clears a row's unseen mark once its seen write succeeded, until its changed time moves past the one it had.
function readAsSeen(row: Row, written: Map<string, number>): Row {
  const writtenAt = written.get(row.key);
  return row.unseen && writtenAt !== undefined && row.changed <= writtenAt ? { ...row, unseen: false } : row;
}

// keepVisited adds the cursor's row to the rows that keep a composer and refreshes each kept row from the list. A row
// missing from the list is kept as it was. It returns previous itself when nothing changes, so an unchanged list does
// not render again.
function keepVisited(previous: Row[], rows: Row[], cursorRow: Row | null): Row[] {
  const kept = previous.map((held) => rows.find((row) => row.key === held.key) ?? held);
  if (cursorRow !== null && !kept.some((row) => row.key === cursorRow.key)) {
    kept.push(cursorRow);
  }
  const unchanged = kept.length === previous.length && kept.every((row, position) => row === previous[position]);
  return unchanged ? previous : kept;
}

// composerRows returns the rows that render a composer: the visited rows, and the cursor's row before the effect has
// added it.
function composerRows(visited: Row[], cursorRow: Row | null): Row[] {
  if (cursorRow === null || visited.some((row) => row.key === cursorRow.key)) {
    return visited;
  }
  return [...visited, cursorRow];
}

// resolveIndex finds the cursor's row, or the index it sat at, clamped into the rows; -1 when there are none.
function resolveIndex(rows: Row[], cursor: Cursor): number {
  if (rows.length === 0) {
    return -1;
  }
  const found = rows.findIndex((row) => row.key === cursor.key);
  return found >= 0 ? found : clamp(cursor.index, rows.length);
}

function clamp(index: number, length: number): number {
  return Math.min(Math.max(index, 0), length - 1);
}

function sectionTitle(section: Section): string {
  if (section.title !== "") {
    return section.title;
  }
  return section.rows[0].host || noDirectoryTitle;
}

function lastPathElement(path: string): string {
  const parts = path.split("/").filter((part) => part !== "");
  return parts[parts.length - 1] ?? "";
}

function ageText(changed: number, now: number): string {
  if (changed <= 0) {
    return "";
  }
  const seconds = Math.max(0, now - changed);
  if (seconds < secondsPerMinute) {
    return `${seconds}s`;
  }
  const minutes = Math.floor(seconds / secondsPerMinute);
  if (minutes < minutesPerHour) {
    return `${minutes}m`;
  }
  const hours = Math.floor(minutes / minutesPerHour);
  if (hours < hoursPerDay) {
    return `${hours}h`;
  }
  return `${Math.floor(hours / hoursPerDay)}d`;
}
