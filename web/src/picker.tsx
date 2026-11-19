import { Code, ConnectError } from "@connectrpc/connect";
import { useEffect, useRef, useState } from "preact/hooks";

import * as actions from "./actions";
import type { Clients } from "./actions";
import type { PickerRow } from "./gen/orrery/v1/orrery_pb";
import type { Row } from "./rows";
import { listedTitle, TitleParts } from "./titleparts";

// The one line a refused or failed Start leaves. It never shows the server's text.
const pickerFailedText = "the picker failed";

// The kinds of row that name a folder. Choosing one opens the start form for its directory.
const folderKinds = ["pane", "fresh"];

// kindWords is the word each kind of result leads with. A kind not listed here leads with no word.
const kindWords: ReadonlyMap<string, string> = new Map([
  ["jump", "go to"],
  ["resume", "resume"],
  ["continue", "continue"],
  ["pane", "start in"],
  ["fresh", "start in"],
]);

// pickerTitle is the title a picker row draws. A row with a pane takes its listed pane row's directory and summary when a
// listed pane row matches it. Any other row takes its own directory and served label.
function pickerTitle(row: PickerRow, rows: Row[]): { dir: string; line: string } {
  const listed = row.pane !== "" && rows.some((candidate) => candidate.kind === "pane" && candidate.id === row.pane);
  return listed ? listedTitle(rows, "pane", row.pane, row.label) : { dir: row.dir, line: row.label };
}

// askFailures are the sentences a failed Ask shows, by the Connect code the call failed with. A code not listed shows
// askFailedText, and the server's own text is never shown.
const askFailures: ReadonlyMap<Code, string> = new Map([
  [Code.DeadlineExceeded, "The search did not finish: the server did not answer in 90 seconds."],
  [Code.Unavailable, "The search did not finish: the server could not answer."],
  [Code.NotFound, "New is not set up on this server."],
]);
const askFailedText = "The search did not finish.";

// askFailureOf is the sentence a failed Ask shows for error.
function askFailureOf(error: unknown): string {
  return askFailures.get(ConnectError.from(error).code) ?? askFailedText;
}

// runFailures are the sentences a failed Run shows, by the Connect code it failed with. A code not listed shows
// runFailedText, and the server's own text is never shown.
const runFailures: ReadonlyMap<Code, string> = new Map([
  [Code.NotFound, "That result has gone stale. Ask again."],
  [Code.FailedPrecondition, "It did not open: the result expired, or tmux is not running on the server."],
  [Code.Unavailable, "It did not open: the server could not run it."],
]);
const runFailedText = "It did not open.";

// runFailureOf is the sentence a failed Run shows for error.
function runFailureOf(error: unknown): string {
  return runFailures.get(ConnectError.from(error).code) ?? runFailedText;
}

// newHint is the note the field shows before anything is asked.
const newHint = "Name a directory to start a session there, or say a few words about a session to find it.";

// NewPanel is New session: a panel over the page, or a sheet on a narrow window. Enter in its field sends the words, as
// typed, to Picker.Ask, and only while the field is not blank and no ask runs. The rows that come back are listed, each
// with its kind's word, its title, drawn through TitleParts from the listed rows, a chip when it starts with the words,
// and its note. The first row is current, and ↑ and ↓ in the field move the current row, wrapping at both ends. Enter
// chooses the current row while the field holds the words last asked, and asks again once they change. Choosing a row
// that is not a folder sends its token to Picker.Run and reports the pane the run answered to onRun. Choosing a folder
// row opens StartForm in place of the list, and the session the form starts is reported to onStart. Escape closes it
// through onClose.
export function NewPanel({
  clients,
  rows,
  sheet,
  onRun,
  onStart,
  onClose,
}: {
  clients: Clients;
  rows: Row[];
  sheet: boolean;
  onRun: (pane: string) => void;
  onStart: (session: string) => void;
  onClose: () => void;
}) {
  const [words, setWords] = useState("");
  // The words the panel last sent to Picker.Ask, which a prompt row's start form starts with.
  const [asked, setAsked] = useState("");
  const [asking, setAsking] = useState(false);
  // The rows the last Ask answered, or null before an answer. An Ask that runs has none.
  const [found, setFound] = useState<PickerRow[] | null>(null);
  // The index of the current row in found.
  const [current, setCurrent] = useState(0);
  const [askError, setAskError] = useState("");
  // The folder row whose start form is open, if one is.
  const [folder, setFolder] = useState<PickerRow | null>(null);
  const [runError, setRunError] = useState("");
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  // listed is the rows the list draws and the arrow keys and Enter act on: none while a folder's start form is open, or
  // before an answer.
  const listed: PickerRow[] = folder === null && found !== null ? found : [];

  const ask = () => {
    if (asking || words.trim() === "") {
      return;
    }
    setAsking(true);
    setAskError("");
    setRunError("");
    setFound(null);
    setCurrent(0);
    setFolder(null);
    setAsked(words);
    void actions.ask(clients, words).then(
      (answer) => {
        setFound(answer);
        setAsking(false);
      },
      (error: unknown) => {
        setAskError(askFailureOf(error));
        setAsking(false);
      },
    );
  };

  const choose = (row: PickerRow) => {
    if (folderKinds.includes(row.kind)) {
      setFolder(row);
      return;
    }
    setRunError("");
    void actions.run(clients, row.token).then(onRun, (error: unknown) => setRunError(runFailureOf(error)));
  };

  // enter chooses the current row while the field holds the words last asked, and asks otherwise.
  const enter = () => {
    if (listed.length > 0 && words === asked) {
      choose(listed[current]);
      return;
    }
    ask();
  };

  // step moves the current row by one, wrapping from the last row to the first and back.
  const step = (by: number) => {
    setCurrent((index) => (index + by + listed.length) % listed.length);
  };

  const closeOnEscape = (event: KeyboardEvent) => {
    if (event.key === "Escape") {
      onClose();
    }
  };

  // note is the one line in the hint's place: a run's failure, an ask's failure, the hint before any ask, or nothing
  // found. While an ask runs it is empty.
  const note =
    runError !== "" ? (
      <p class="inote error" role="alert">
        {runError}
      </p>
    ) : askError !== "" ? (
      <p class="inote error" role="alert">
        {askError}
      </p>
    ) : asking ? null : found === null ? (
      <p class="inote">{newHint}</p>
    ) : found.length === 0 ? (
      <p class="inote">{`Nothing found for “${asked}”. Try a directory name.`}</p>
    ) : null;

  const body = (
    <>
      <label class="field">
        <svg class="icon" viewBox="0 0 16 16" aria-hidden="true">
          <path d="M7 3a4 4 0 1 0 0 8 4 4 0 0 0 0-8zM10 10l3.5 3.5" />
        </svg>
        <input
          ref={inputRef}
          value={words}
          placeholder="A directory, a session, or what you want to start"
          aria-label="New session"
          onInput={(event) => setWords(event.currentTarget.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter") {
              enter();
            } else if ((event.key === "ArrowDown" || event.key === "ArrowUp") && listed.length > 0) {
              event.preventDefault();
              step(event.key === "ArrowDown" ? 1 : -1);
            }
          }}
        />
        {asking ? <span class="count">asking…</span> : <kbd>⏎</kbd>}
      </label>
      {note}
      {folder !== null ? (
        <StartForm key={folder.token} clients={clients} folder={folder} asked={asked} onStarted={onStart} />
      ) : (
        found !== null &&
        found.length > 0 && (
          <div class="rows">
            {found.map((row, index) => {
              const title = pickerTitle(row, rows);
              const isCurrent = index === current;
              return (
                <button
                  key={row.token}
                  type="button"
                  class={isCurrent ? "pick-row cur" : "pick-row"}
                  aria-selected={isCurrent}
                  onClick={() => choose(row)}
                >
                  <span class="k">{kindWords.get(row.kind)}</span>
                  <span class="t">
                    <TitleParts dir={title.dir} line={title.line} />
                  </span>
                  {row.prompt && <span class="chip live">starts with your words</span>}
                  <span class="n">{row.note}</span>
                </button>
              );
            })}
          </div>
        )
      )}
    </>
  );

  return sheet ? (
    <div class="sheet-scrim">
      <section class="sheet" role="dialog" aria-label="New session" onKeyDown={closeOnEscape}>
        <div class="sheet-head">
          <b>New session</b>
        </div>
        <div class="sheet-body">{body}</div>
      </section>
    </div>
  ) : (
    <div class="scrim">
      <section class="panel" role="dialog" aria-label="New session" onKeyDown={closeOnEscape}>
        <div class="head">
          <h2 class="t-title">New session</h2>
          <span class="t-small">
            <kbd>N</kbd> or <kbd>⌘K</kbd>
          </span>
          <button type="button" aria-label="Close" title="Close  esc" onClick={onClose}>
            <svg class="icon" viewBox="0 0 16 16" aria-hidden="true">
              <path d="M4 4l8 8M12 4l-8 8" />
            </svg>
          </button>
        </div>
        {body}
      </section>
    </div>
  );
}

// StartForm is the start form a folder row opens. It names the row's folder and holds a message box, which a
// prompt row's box starts with the asked words and any other starts empty. Its start id is made once, when the form
// opens, so every Start it sends, a retry after a failure or an edit included, names the same session. Start sends the
// folder, the message and that id to Control.Start, and onStarted receives the session it started. A refused or failed
// Start keeps the form and the message as typed and shows the fixed line.
function StartForm({
  clients,
  folder,
  asked,
  onStarted,
}: {
  clients: Clients;
  folder: PickerRow;
  asked: string;
  onStarted: (session: string) => void;
}) {
  const [startId] = useState(() => actions.newStartId());
  const [message, setMessage] = useState(folder.prompt ? asked : "");
  const [error, setError] = useState("");

  const start = () => {
    setError("");
    void actions.start(clients, folder.dir, message, startId).then(onStarted, () => setError(pickerFailedText));
  };

  return (
    <div class="picker-start">
      <p class="picker-folder">{folder.dir}</p>
      <textarea value={message} aria-label="message" onInput={(event) => setMessage(event.currentTarget.value)} />
      <button type="button" onClick={start}>
        Start
      </button>
      {error !== "" && (
        <p class="error" role="alert">
          {error}
        </p>
      )}
    </div>
  );
}
