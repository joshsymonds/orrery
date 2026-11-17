import { useEffect, useRef, useState } from "preact/hooks";

import * as actions from "./actions";
import type { Clients } from "./actions";
import type { PickerRow } from "./gen/orrery/v1/orrery_pb";
import type { Row } from "./rows";
import { listedTitle, TitleParts } from "./titleparts";

// The one line a refused or failed Ask or Run leaves. It never shows the server's text.
const pickerFailedText = "the picker failed";

// The kinds of row that name a folder. Choosing one opens the start form for its directory.
const folderKinds = ["pane", "fresh"];

// pickerTitle is the title a picker row draws. A row with a pane takes its listed pane row's directory and summary when a
// listed pane row matches it. Any other row takes its own directory and served label.
function pickerTitle(row: PickerRow, rows: Row[]): { dir: string; line: string } {
  const listed = row.pane !== "" && rows.some((candidate) => candidate.kind === "pane" && candidate.id === row.pane);
  return listed ? listedTitle(rows, "pane", row.pane, row.label) : { dir: row.dir, line: row.label };
}

// PickerPalette is the palette over the page. Enter in its text box sends the words, as typed, to Picker.Ask, and the
// rows that come back are listed with their title, drawn through TitleParts from the listed rows, and their note. Choosing
// a row that is not a folder sends its token to Picker.Run and reports the pane the run answered to onRun. Choosing a
// folder row opens PickerStartForm in place of the list, and the session the form starts is reported to onStart. Escape
// closes the palette through onClose.
export function PickerPalette({
  clients,
  rows,
  onRun,
  onStart,
  onClose,
}: {
  clients: Clients;
  rows: Row[];
  onRun: (pane: string) => void;
  onStart: (session: string) => void;
  onClose: () => void;
}) {
  const [words, setWords] = useState("");
  // The words the palette last sent to Picker.Ask, which a prompt row's start form starts with.
  const [asked, setAsked] = useState("");
  const [found, setFound] = useState<PickerRow[]>([]);
  // The folder row whose start form is open, if one is.
  const [folder, setFolder] = useState<PickerRow | null>(null);
  const [error, setError] = useState("");
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  const ask = () => {
    setError("");
    setAsked(words);
    void actions.ask(clients, words).then(setFound, () => setError(pickerFailedText));
  };

  const choose = (row: PickerRow) => {
    if (folderKinds.includes(row.kind)) {
      setFolder(row);
      return;
    }
    setError("");
    void actions.run(clients, row.token).then(onRun, () => setError(pickerFailedText));
  };

  return (
    <section
      class="picker"
      role="dialog"
      aria-label="picker"
      onKeyDown={(event) => {
        if (event.key === "Escape") {
          onClose();
        }
      }}
    >
      <input
        ref={inputRef}
        value={words}
        placeholder="Find a pane or a directory"
        aria-label="ask"
        onInput={(event) => setWords(event.currentTarget.value)}
        onKeyDown={(event) => {
          if (event.key === "Enter") {
            ask();
          }
        }}
      />
      {error !== "" && (
        <p class="error" role="alert">
          {error}
        </p>
      )}
      {folder !== null ? (
        <PickerStartForm key={folder.token} clients={clients} folder={folder} asked={asked} onStarted={onStart} />
      ) : (
        <div class="picker-rows">
          {found.map((row) => {
            const title = pickerTitle(row, rows);
            return (
              <button key={row.token} type="button" class="picker-row" onClick={() => choose(row)}>
                <span class="picker-label">
                  <TitleParts dir={title.dir} line={title.line} />
                </span>
                <span class="picker-note">{row.note}</span>
              </button>
            );
          })}
        </div>
      )}
    </section>
  );
}

// PickerStartForm is the start form a folder row opens. It names the row's folder and holds a message box, which a
// prompt row's box starts with the asked words and any other starts empty. Its start id is made once, when the form
// opens, so every Start it sends, a retry after a failure or an edit included, names the same session. Start sends the
// folder, the message and that id to Control.Start, and onStarted receives the session it started. A refused or failed
// Start keeps the form and the message as typed and shows the fixed line.
export function PickerStartForm({
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
