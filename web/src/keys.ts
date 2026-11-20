// Where is a context a key acts in: the list's page (the list, the inbox at rest, and the help panel), or one of the
// other places a key reaches.
export type Where =
  | "list"
  | "inbox-at-rest"
  | "help"
  | "palette"
  | "reply-box"
  | "epic-page"
  | "reply-empty"
  | "reply-typed"
  | "step-box";

// Chord is one key press: its key, and each modifier it holds.
export interface Chord {
  key: string;
  shift?: true;
  ctrl?: true;
  alt?: true;
  meta?: true;
}

// KeyLine is one line of the table: its key caps, what the key does, and the chords it stands for in each context.
export interface KeyLine {
  caps: string[];
  text: string;
  acts: { chords: Chord[]; in: Where[] }[];
}

// KeyGroup is one titled group of the table.
export interface KeyGroup {
  title: "Moving" | "The inbox" | "Reading" | "Answering and stopping" | "Everywhere";
  lines: KeyLine[];
}

// page is the list's page: the list, the inbox at rest, and the help panel, which show the same keys.
const page: Where[] = ["list", "inbox-at-rest", "help"];
// boxes is the send and new-line keys of the boxes that send: the empty reply box, the typed one and the step box.
const boxes: Where[] = ["reply-empty", "reply-typed", "step-box"];

// keyGroups is the table of every key the web answers to, in its five groups.
export const keyGroups: readonly KeyGroup[] = [
  {
    title: "Moving",
    lines: [
      {
        caps: ["j", "k", "↓", "↑"],
        text: "down and up the inbox",
        acts: [{ chords: [{ key: "j" }, { key: "k" }, { key: "ArrowDown" }, { key: "ArrowUp" }], in: page }],
      },
      {
        caps: ["g g", "G"],
        text: "first and last row",
        acts: [{ chords: [{ key: "g" }, { key: "G" }], in: page }],
      },
      { caps: ["w"], text: "next thing that waits on you", acts: [{ chords: [{ key: "w" }], in: page }] },
      { caps: ["n"], text: "next new row", acts: [{ chords: [{ key: "n" }], in: page }] },
      {
        caps: ["h", "←"],
        text: "back one page",
        acts: [{ chords: [{ key: "h" }, { key: "ArrowLeft" }], in: page }],
      },
      {
        caps: ["⏎"],
        text: "open the row and go to its reply box",
        acts: [{ chords: [{ key: "Enter" }], in: page }],
      },
      {
        caps: ["⇧ ⇥"],
        text: "from a reply box, back to the inbox",
        acts: [{ chords: [{ key: "Tab", shift: true }], in: ["reply-box"] }],
      },
    ],
  },
  {
    title: "The inbox",
    lines: [
      {
        caps: ["b"],
        text: "bring the inbox out over the page; again to tuck it back",
        acts: [{ chords: [{ key: "b" }], in: page }],
      },
      { caps: ["B"], text: "pin the inbox beside the page", acts: [{ chords: [{ key: "B" }], in: page }] },
      {
        caps: ["/"],
        text: "filter the inbox: ⏎ leaves it, esc clears it",
        acts: [{ chords: [{ key: "/" }], in: page }],
      },
      { caps: ["v"], text: "archived", acts: [{ chords: [{ key: "v" }], in: page }] },
      { caps: ["A"], text: "archive the row", acts: [{ chords: [{ key: "A" }], in: page }] },
      { caps: ["x"], text: "mark it seen", acts: [{ chords: [{ key: "x" }], in: page }] },
      {
        caps: ["e"],
        text: "mark it seen and go to the next new row",
        acts: [{ chords: [{ key: "e" }], in: page }],
      },
      {
        caps: ["N", "⌘K", "Ctrl+K"],
        text: "new session, or find one",
        acts: [
          { chords: [{ key: "N" }], in: page },
          {
            chords: [
              { key: "k", meta: true },
              { key: "K", meta: true },
              { key: "k", ctrl: true },
              { key: "K", ctrl: true },
            ],
            in: [...page, "palette", "reply-box"],
          },
        ],
      },
    ],
  },
  {
    title: "Reading",
    lines: [
      { caps: ["i"], text: "go to the reply box", acts: [{ chords: [{ key: "i" }], in: page }] },
      {
        caps: ["/"],
        text: "in an empty reply box: find in the conversation",
        acts: [{ chords: [{ key: "/" }], in: ["reply-empty"] }],
      },
      {
        caps: ["⇞", "⇟"],
        text: "in an empty reply box: page the conversation",
        acts: [{ chords: [{ key: "PageUp" }, { key: "PageDown" }], in: ["reply-empty"] }],
      },
      { caps: ["o"], text: "open or close a compaction", acts: [{ chords: [{ key: "o" }], in: page }] },
      {
        caps: ["|"],
        text: "pin the row in a second column, or close the pin",
        acts: [{ chords: [{ key: "|" }], in: page }],
      },
      { caps: ["m"], text: "hide or show the meters", acts: [{ chords: [{ key: "m" }], in: page }] },
      // The find bar's keys are written by hand: find.tsx handles them, and the sweep does not press them.
      { caps: ["⏎", "⇧ ⏎", "esc"], text: "in find: next match, previous match, close find", acts: [] },
    ],
  },
  {
    title: "Answering and stopping",
    lines: [
      {
        caps: ["1", "2", "3"],
        text: "answer the cursor's question with that option",
        acts: [{ chords: [{ key: "1" }, { key: "2" }, { key: "3" }], in: page }],
      },
      {
        caps: ["1", "…", "9"],
        text: "on an epic's page: pick that option",
        acts: [{ chords: Array.from({ length: 9 }, (_, index) => ({ key: String(index + 1) })), in: ["epic-page"] }],
      },
      { caps: ["⏎"], text: "send", acts: [{ chords: [{ key: "Enter" }], in: boxes }] },
      {
        caps: ["⇧ ⏎", "⌥ ⏎"],
        text: "a new line",
        acts: [{ chords: [{ key: "Enter", shift: true }, { key: "Enter", alt: true }], in: boxes }],
      },
      {
        caps: ["↑"],
        text: "in an empty reply box: take the queued messages back to edit",
        acts: [{ chords: [{ key: "ArrowUp" }], in: ["reply-empty"] }],
      },
      {
        caps: ["esc"],
        text: "take queued messages back, then interrupt a working turn",
        acts: [{ chords: [{ key: "Escape" }], in: ["list", "inbox-at-rest", "reply-box"] }],
      },
    ],
  },
  {
    title: "Everywhere",
    lines: [
      { caps: ["?"], text: "this list", acts: [{ chords: [{ key: "?" }], in: page }] },
      {
        caps: ["esc"],
        text: "close a menu, a panel, or this list",
        acts: [{ chords: [{ key: "Escape" }], in: ["help", "palette"] }],
      },
    ],
  },
];
