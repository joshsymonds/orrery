import { useEffect, useRef, useState } from "preact/hooks";

import * as actions from "./actions";
import type { Clients } from "./actions";
import type { PickerRow } from "./gen/orrery/v1/orrery_pb";

// The one line a refused or failed Ask or Run leaves. It never shows the server's text.
const pickerFailedText = "the picker failed";

// The kinds of row that name a folder. Choosing one does nothing yet: the start form opens it in a later task.
const folderKinds = ["pane", "fresh"];

// PickerPalette is the palette over the page. Enter in its text box sends the words, as typed, to Picker.Ask, and the
// rows that come back are listed with their label and note. Choosing a row that is not a folder sends its token to
// Picker.Run and reports the pane the run answered to onRun. Escape closes the palette through onClose.
export function PickerPalette({
  clients,
  onRun,
  onClose,
}: {
  clients: Clients;
  onRun: (pane: string) => void;
  onClose: () => void;
}) {
  const [words, setWords] = useState("");
  const [rows, setRows] = useState<PickerRow[]>([]);
  const [error, setError] = useState("");
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  const ask = () => {
    setError("");
    void actions.ask(clients, words).then(setRows, () => setError(pickerFailedText));
  };

  const choose = (row: PickerRow) => {
    if (folderKinds.includes(row.kind)) {
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
      <div class="picker-rows">
        {rows.map((row) => (
          <button key={row.token} type="button" class="picker-row" onClick={() => choose(row)}>
            <span class="picker-label">{row.label}</span>
            <span class="picker-note">{row.note}</span>
          </button>
        ))}
      </div>
    </section>
  );
}
