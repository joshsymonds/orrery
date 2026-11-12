import { useEffect, useRef, useState } from "preact/hooks";

import { byAttention, byProject, type Row, type Section } from "./rows";
import { ReplyText } from "./text";

type View = "attention" | "project";

// The cursor is held by row key, so it survives new rows. index is where it sat, used when its row leaves.
interface Cursor {
  key: string;
  index: number;
}

const stateWaiting = "waiting";
const noDirectoryTitle = "no directory";

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
  [["/"], "filter: Enter leaves it, Esc clears it"],
  [["?"], "toggle this help"],
  [["Esc"], "close this help"],
];

export function App({ rows, error, now }: { rows: Row[]; error: string; now: number }) {
  const [view, setView] = useState<View>("attention");
  const [cursor, setCursor] = useState<Cursor>({ key: "", index: 0 });
  const [filter, setFilter] = useState("");
  const [helpOpen, setHelpOpen] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);
  const pendingG = useRef(false);
  const handler = useRef<(event: KeyboardEvent) => void>();

  const words = filter
    .toLowerCase()
    .split(/\s+/)
    .filter((word) => word !== "");
  const matching = rows.filter((row) => matchesFilter(row, words));
  const sections = view === "attention" ? byAttention(matching) : byProject(matching);
  const flat = sections.flatMap((section) => section.rows);
  const index = resolveIndex(flat, cursor);
  const cursorRowKey = index >= 0 ? flat[index].key : "";
  const shownIndex = Math.max(index, 0);

  // The held cursor follows the row shown, so a first render or a clamp does not leave it on a stale key.
  useEffect(() => {
    if (cursor.key !== cursorRowKey || cursor.index !== shownIndex) {
      setCursor({ key: cursorRowKey, index: shownIndex });
    }
  }, [cursor, cursorRowKey, shownIndex]);

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
      case "/":
        inputRef.current?.focus();
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
    </main>
  );
}

// matchesFilter keeps a row whose label, project, host, state or summary holds every typed word.
function matchesFilter(row: Row, words: string[]): boolean {
  const haystack = [row.label, row.project, row.host, row.state, row.summary].join(" ").toLowerCase();
  return words.every((word) => haystack.includes(word));
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
