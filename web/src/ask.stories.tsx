// The stories of Ask and its folded line, one for each state round 6's Ask draws, in round 6's words. Each names its round-6
// Ask/State in its name, so the catalog's round-6 check finds it.

import type { JSX } from "preact";

import * as drawing from "./ask";
import type { AskModel, AskOption } from "./askmodel";
import type { Stories } from "./catalog/story";
import { at } from "./fixtures/clock";

const now = Number(at("@14:41"));
const fourMinutes = 240;
const twoMinutes = 120;
const fiveDays = 5 * 86400;

const noop = () => {};

// confirmAnswer records an epic's answer with no error, as an answer that is sent does.
const confirmAnswer = async () => "";

const sessionOptions: AskOption[] = [
  {
    n: 1,
    name: "One query per week",
    does: "Join keepers in the week query. Smallest change; touches query.go only.",
    recommended: true,
    why: "",
    answerable: true,
  },
  {
    n: 2,
    name: "Cache keepers for the request",
    does: "Keeps the row code as it is. Adds a per-request map.",
    recommended: false,
    why: "",
    answerable: true,
  },
  {
    n: 3,
    name: "Paginate the page",
    does: "Twenty shifts a page. Changes what the harbour office sees.",
    recommended: false,
    why: "",
    answerable: true,
  },
];

const epicOptions: AskOption[] = [
  {
    n: 1,
    name: "Keep the pin",
    does: "Skip the Lisbon case. The contract holds: R5 is met for every port on today's rota.",
    recommended: true,
    why: "Lisbon is not on any rota until March",
    answerable: true,
  },
  {
    n: 2,
    name: "Allow the bump",
    does: "A contract change. Adds go.mod to this task and reruns the full gate.",
    recommended: false,
    why: "",
    answerable: true,
  },
];

// sessionAsk is the open question of tidepool's session, which has waited four minutes and can be archived.
function sessionAsk(init: Partial<AskModel> = {}): AskModel {
  return {
    kind: "session",
    who: "tidepool · Why the roster page is slow past forty shifts",
    asked: now - fourMinutes,
    question: "Which fix should I make?",
    command: null,
    options: sessionOptions,
    putOff: false,
    archive: { kind: "headless", id: "tidepool" },
    target: {
      kind: "headless",
      id: "tidepool",
      requestId: "r1",
      options: sessionOptions.map((option) => option.name),
    },
    epic: null,
    ...init,
  };
}

// epicAsk is the epic question of rota-export, which recommends Keep the pin.
function epicAsk(): AskModel {
  return {
    kind: "epic",
    who: "lighthouse · rota-export",
    asked: now - twoMinutes,
    question:
      "Bump the embedded tz database?\n\nThe pinned database lacks the 2026 Lisbon rule, so one fixture cannot pass. A bump changes go.mod, which is outside this task’s owned files, and the contract says no dependency changes.",
    command: null,
    options: epicOptions,
    putOff: false,
    archive: null,
    target: null,
    epic: { id: "rota-export", questionId: "Q7" },
  };
}

// ask draws an Ask of model in the desk layout, with every control its owner can press.
function ask(model: AskModel, init: Partial<drawing.AskProps> = {}): JSX.Element {
  return (
    <drawing.Ask
      model={model}
      now={now}
      phase="open"
      picked={null}
      note=""
      keyCaps
      narrow={false}
      numberKeys={false}
      onAnswer={noop}
      onNotNow={noop}
      onBringBack={noop}
      onDecline={noop}
      onArchive={noop}
      onConfirm={confirmAnswer}
      {...init}
    />
  );
}

export const Ask: Stories = {
  session: () => ask(sessionAsk()),
  permission: () =>
    ask(
      sessionAsk({
        kind: "permission",
        question: "Run this command?",
        command: { tool: "Bash", text: "git push --force-with-lease" },
        options: [
          { n: 1, name: "Yes", does: "", recommended: false, why: "", answerable: true },
          {
            n: 2,
            name: "Yes, and don’t ask again",
            does: "for commands like this one in this session",
            recommended: false,
            why: "",
            answerable: true,
          },
          {
            n: 3,
            name: "No",
            does: "and say what to do instead, below",
            recommended: false,
            why: "",
            answerable: true,
          },
        ],
        target: null,
        archive: null,
      }),
    ),
  epic: () => ask(epicAsk()),
  "epic-something-else": () =>
    ask(epicAsk(), { pick: { choice: "other", text: "Vendor the Lisbon rule as a fixture file." } }),
  sending: () => ask(sessionAsk(), { phase: "sending", picked: 1 }),
  error: () =>
    ask(sessionAsk(), {
      phase: "error",
      note: "Not answered: the question changed before your answer arrived. This is what it asks now.",
    }),
  stale: () => ask(sessionAsk({ asked: now - fiveDays })),
  unanswerable: () => ask(sessionAsk({ options: [] })),
  answered: () => <drawing.AskLine state="answered" n={1} name="One query per week" question="Which fix should I make?" keyCaps />,
  settled: () => <drawing.AskLine state="settled" who="lighthouse · rota-export" />,
};
