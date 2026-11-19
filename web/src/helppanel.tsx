// helpEntries are the keys the help panel lists: each entry's keys, and what they do.
const helpEntries: [string[], string][] = [
  [["j", "k", "↓", "↑"], "move the cursor one row up or down"],
  [["gg"], "first row"],
  [["G"], "last row"],
  [["a"], "by attention"],
  [["p"], "by project"],
  [["v"], "archived rows and epics"],
  [["A"], "archive the cursor's row or epic"],
  [["w"], "first waiting row"],
  [["n"], "next unseen row, wrapping"],
  [["N"], "open the palette to ask for a pane or a directory, outside a text box"],
  [["⌘K"], "open the palette anywhere, a text box included; Ctrl+K does too"],
  [["x", "Enter"], "mark the cursor's row seen; Enter also opens the row into its reply box"],
  [["e"], "mark it seen, then go to the next unseen row"],
  [["/"], "filter: Enter leaves it, Esc clears it"],
  [["i"], "focus the reply box"],
  [["Shift+Tab"], "from a reply box, back to the list's cursor row, taking nothing back"],
  [["Page Up", "Page Down"], "in an empty reply box, page the transcript"],
  [["/"], "in an empty reply box, find in the transcript"],
  [["1", "2", "3"], "answer the cursor's waiting question with that choice, or pick that option on an epic page"],
  [["o"], "open or close a compaction's summary"],
  [["|"], "pin the cursor's row in a second column, or close the pin"],
  [["b"], "pop the inbox out over the page, or tuck it back"],
  [["B"], "pin or unpin the inbox beside the page"],
  [["h", "←"], "back one crumb, or the inbox from the first crumb"],
  [["m"], "hide or show the strips"],
  [["?"], "toggle this help"],
  [["Esc"], "close this help"],
];

// KeyList draws the keys section App shows while help is open: each entry's keys, and what they do.
export function KeyList() {
  return (
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
  );
}
