// The stories that bear no round-6 name, each with the round-6 component or scenario that draws it and the frontend epic that
// draws that. An entry is keyed by a story's name, or by an export's kebab name when the export is not a round-6 component
// and no story of it bears a name: that entry covers every story of the export. A component's stories are listed one by one.
// names.test.ts checks the table against the registry and round6.json.

import type { UnnamedEntry } from "./names";

export const unnamed: Readonly<Record<string, UnnamedEntry>> = {
  // ---- whole exports: the export is not a round-6 component, and each of its stories is unnamed ----

  // App draws each kind of page at an address: the inbox, the archive, a session, a pane, a step, an epic, a contract, a
  // phase, a worker session. The inbox page stands for them.
  app: { replacement: "inbox-home", epic: "F2" },
  // Mark draws the Orrery mark, the symbol the rail and the title row open on.
  mark: { replacement: "Tile", epic: "F2" },
  // RailLineage draws a project's lineage under its tile on the collapsed inbox's rail: a pill per conversation, a pill per design,
  // and a mark for each contract or epic. The rail's tiles stand for it.
  "rail-lineage": { replacement: "Tile", epic: "F2" },
  // ReaderHead draws a conversation's head, titled, or with where.
  "reader-head": { replacement: "TitleRow", epic: "F3" },
  // TitleParts draws a title's directory and line, with either alone, and a line that holds a link.
  "title-parts": { replacement: "TitleRow", epic: "F3" },
  // OutputTails draws a command's output tails: both, stdout only, stderr only.
  "output-tails": { replacement: "epic-evidence", epic: "F4" },
  // EpicWidget draws the reader's tool-call card: the live card of an epic a tool call created, under the conversation's tool call.
  "epic-widget": { replacement: "convo-epic", epic: "F3" },
  // AccountsPage draws the accounts and routes page at #/accounts, as App draws the pages at its addresses. The inbox page
  // stands for it.
  "accounts-page": { replacement: "inbox-home", epic: "F2" },
  // AccountsTable draws the accounts table of the accounts and routes page, which round 6 does not draw. The inbox page
  // stands for it, as it does for AccountsPage.
  "accounts-table": { replacement: "inbox-home", epic: "F2" },
  // AccountAdd draws the Add account form under the accounts table, which round 6 does not draw. The inbox page stands for
  // it, as it does for AccountsPage.
  "account-add": { replacement: "inbox-home", epic: "F2" },
  // RouteContexts draws the contexts section of the accounts and routes page, which round 6 does not draw. The inbox page
  // stands for it, as it does for AccountsPage.
  "route-contexts": { replacement: "inbox-home", epic: "F2" },
  // RouteSeats draws the seats table of the accounts and routes page, which round 6 does not draw. The inbox page stands for
  // it, as it does for AccountsPage.
  "route-seats": { replacement: "inbox-home", epic: "F2" },
  // AccountsNarrow draws the accounts and routes page under 760px, which round 6 does not draw. The inbox page stands for it,
  // as it does for AccountsPage.
  "accounts-narrow": { replacement: "inbox-home", epic: "F2" },
  // ActivityLines draws what runs under a conversation as lines above its composer, with a count past three. Round 6
  // draws each running thing as a task line in the transcript, and no line above the composer.
  "activity-lines": { replacement: "TaskLine", epic: "F3" },
  // WorkLink draws what a conversation is working on as plain links in its title row, after the title, with a "+n" control
  // for the rest. Round 6 names what a conversation steers in its rail's tiles, and no link in the title row.
  "work-link": { replacement: "TitleRow", epic: "F3" },
  // HeldCard draws the held card at the desk: the design, contract or epic held open, as a rounded card set into its
  // conversation on the right. Round 6 draws a conversation's epics and contract as its Work cards.
  "held-card": { replacement: "WorkCard", epic: "F4" },
  // HeldSheet draws the held thing where the desk's card does not fit, as a sheet over the conversation. Round 6 draws a
  // panel for a thing as a sheet.
  "held-sheet": { replacement: "Sheet", epic: "F4" },
  // DesignCard draws a design held at the desk: its state, then its contracts in position order, each with its state. Round 6
  // draws a design's contracts as Work cards, and no design's record.
  "design-card": { replacement: "WorkCard", epic: "F4" },
  // ContractWork draws an accepted contract's work by requirement: the epic's door, then each requirement with its state,
  // its tasks and its proof. Round 6 draws an accepted contract as its document, and no work by requirement.
  "contract-work": { replacement: "contract-accepted", epic: "F4" },
  // QuestionThread draws one design question with its own thread, its own answer box and Accept, in the conversation and in the
  // design. Round 6 draws the Ask alone, with no thread and no box of its own.
  "question-thread": { replacement: "Ask", epic: "F3" },
  // DesignSteelman draws a design's steelman: its budget's two cells in words, each discovery and closure pass, and the ledger
  // of its findings with who decided each. Round 6 draws no design's steelman; its stage track stands nearest, for the budget.
  "design-steelman": { replacement: "Track", epic: "F4" },
  // DesignPage draws a design's page: its stage strip, what the Director decided for you, its contracts, its steelman, its
  // approach, its questions, its research and its log. Round 6 draws no design's page, and its stage track stands nearest,
  // for the strip.
  "design-page": { replacement: "Track", epic: "F4" },
  // UsagePanel draws a conversation's Usage panel in the right rail: its list total, its four token kinds, and its model-and-agent
  // rows. Round 6 draws no usage panel, and ListPanel stands nearest, for a rail panel in the same frame.
  "usage-panel": { replacement: "ListPanel", epic: "F4" },

  // ---- single stories of round-6 components, each the component in a state round 6 does not draw ----

  // HappeningList draws the history of the holder epic, and an empty history.
  "happening-list/holder": { replacement: "HappeningList", epic: "F4" },
  "happening-list/empty": { replacement: "HappeningList", epic: "F4" },
  // TitleRow draws a crumb and a state, a blocker, and a page's commands.
  "title-row/crumbs-and-state": { replacement: "TitleRow", epic: "F3" },
  "title-row/blocker": { replacement: "TitleRow", epic: "F3" },
  "title-row/with-commands": { replacement: "TitleRow", epic: "F3" },
  // StateGlyph draws broken, a ring cracked through, for a failed row and an unbound pane: round 6 draws no such state.
  "state-glyph/broken": { replacement: "StateGlyph", epic: "F2" },
  // ConvoRow draws a conversation that holds what it opened, its block the tab and its row and the open row necks: round 6
  // draws a conversation's row and what it steers, and no held thing.
  "convo-row/held": { replacement: "ConvoRow", epic: "F4" },
};
