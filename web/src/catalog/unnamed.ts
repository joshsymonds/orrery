// The stories that bear no round-6 name, each with the round-6 component or scenario that draws it and the frontend epic that
// draws that. An entry is keyed by a story's name, or by an export's kebab name when the export is not a round-6 component
// and no story of it bears a name: that entry covers every story of the export. A component's stories are listed one by one.
// names.test.ts checks the table against the registry and round6.json.

import type { UnnamedEntry } from "./names";

export const unnamed: Readonly<Record<string, UnnamedEntry>> = {
  // ---- whole exports: the export is not a round-6 component, and each of its stories is unnamed ----

  // App draws each kind of page at an address: attention, a project, the archive, a session, a pane, a step, an epic, a
  // contract, a phase, a worker session. The inbox page stands for them.
  app: { replacement: "inbox-home", epic: "F2" },
  // NowBlock draws an epic's now sentence over its blocker: an open question, a held step, a park, or nothing to block it.
  "now-block": { replacement: "epic-question", epic: "F4" },
  // RowMenu draws the ⋯ menu of an epic line, shut, open, and open on an archived line.
  "row-menu": { replacement: "Menu", epic: "F2" },
  // EpicPage draws an epic's page in each state the fixtures hold: a rota decision, a released holder, a contract with tide
  // alerts, a stopped epic offline, and paused follow-ups.
  "epic-page": { replacement: "epic-building", epic: "F4" },
  // NowRows and NextRows draw the in-flight and planned wedges of an epic, with or without a phase that opens.
  "now-rows": { replacement: "epic-building", epic: "F4" },
  "next-rows": { replacement: "epic-building", epic: "F4" },
  // Handover draws an epic's handover, with its epic-stage review verdicts.
  handover: { replacement: "epic-handover", epic: "F4" },
  // Mark draws the Orrery mark, the symbol the rail and the title row open on.
  mark: { replacement: "Tile", epic: "F2" },
  // Markdown draws reply prose: emphasis, a list, a fenced code block, a table, a heading with a link, and a marked query.
  markdown: { replacement: "Reply", epic: "F3" },
  // MarkedText draws text with its query's occurrences marked, the current one among them.
  "marked-text": { replacement: "convo-find", epic: "F3" },
  // ReplyText draws reply text whose web links are links and whose other schemes stay text.
  "reply-text": { replacement: "Reply", epic: "F3" },
  // NeedEntry draws an epic's need: a question that opens it, a park with Retry, a release with Retry and Stop, or a need that
  // is only its sentence.
  "need-entry": { replacement: "epic-question", epic: "F4" },
  // PhaseOutputPage draws a task's phase output at each phase of the rota export.
  "phase-output-page": { replacement: "phase-implement", epic: "F4" },
  // GreenProof and RedProof draw the green and red cards of an epic page, reached or dim.
  "green-proof": { replacement: "EvidenceRun", epic: "F4" },
  "red-proof": { replacement: "EvidenceRun", epic: "F4" },
  // ReleaseProof draws the release steps of an epic: released, in progress, waiting, or with no contract.
  "release-proof": { replacement: "ReleaseTable", epic: "F4" },
  // Report draws an epic's report: written, not shipped, or unwritten.
  report: { replacement: "epic-shipped", epic: "F4" },
  // Reader draws one conversation: loading, a pane's conversation, a folded compaction, a question, find, a sending box, live
  // session text, and a session's step.
  reader: { replacement: "convo-shell", epic: "F3" },
  // ReaderColumn draws a row's column: its head, reader and send box, or the cursor's step, a held step, a loading session.
  "reader-column": { replacement: "convo-shell", epic: "F3" },
  // ReaderHead draws a conversation's head, titled, or with where.
  "reader-head": { replacement: "TitleRow", epic: "F3" },
  // Talk draws the epic's Talk command, shut and open, with and without sessions or a repository.
  talk: { replacement: "epic-talk", epic: "F4" },
  // TitleParts draws a title's directory and line, with either alone, and a line that holds a link.
  "title-parts": { replacement: "TitleRow", epic: "F3" },
  // WorkerSessionPage draws a step's worker session while it runs, is held, has ended, or loads.
  "worker-session-page": { replacement: "worker-running", epic: "F4" },
  // OutputTails draws a command's output tails: both, stdout only, stderr only.
  "output-tails": { replacement: "epic-evidence", epic: "F4" },
  // Workflow draws an epic's workflow nodes: a plan and a build, evidence, failed checks, a loop.
  workflow: { replacement: "epic-building", epic: "F4" },
  // ContractBody draws an epic's accepted contract, read-only, with its requirements: with additions, with none, and with no base.
  "contract-body": { replacement: "contract-accepted", epic: "F4" },

  // ---- single stories of round-6 components, each the component in a state round 6 does not draw ----

  // TaskLine draws a background task that is still running: an agent, and a shell. Round 6 draws only ended tasks.
  "task-line/agent-running": { replacement: "TaskLine", epic: "F3" },
  "task-line/shell-running": { replacement: "TaskLine", epic: "F3" },
  // Ask draws an open question of an epic that has no choices, so its answer is typed.
  "ask/open-question": { replacement: "Ask", epic: "F3" },
  // WorkCard draws an epic's card on a project's page when the epic has released, and when the fixtures hold no detail for it.
  "work-card/released": { replacement: "WorkCard", epic: "F2" },
  "work-card/unavailable": { replacement: "WorkCard", epic: "F2" },
  // TalkDock draws the talk dock following a pane, following a worker session, cut off, and following a session loading.
  "talk-dock": { replacement: "epic-talk", epic: "F4" },
  // EpicRow draws an epic's line: the cursor on it, its ⋯ menu shut, and an epic with no detail, so no orrery.
  "epic-row/current": { replacement: "EpicRow", epic: "F2" },
  "epic-row/with-menu": { replacement: "EpicRow", epic: "F2" },
  "epic-row/no-detail": { replacement: "EpicRow", epic: "F2" },
  // EpicEventBlock draws an epic's question event whose epic holds no open question, so the event reads it as settled.
  "epic-event-block/question-settled": { replacement: "EpicEventBlock", epic: "F3" },
  // StepEndBlock draws a step that ended with no agent named and a looping reason.
  "step-end-block/without-agent": { replacement: "StepEndBlock", epic: "F3" },
  // EvidenceRun draws a requirement's captured red and green runs as one list, and a run still going, which draws no exit.
  "evidence-run/captured-red-and-green": { replacement: "EvidenceRun", epic: "F4" },
  "evidence-run/running": { replacement: "EvidenceRun", epic: "F4" },
  // FindBar draws the find field with no query, and with a query that has one match.
  "find-bar/empty": { replacement: "FindBar", epic: "F3" },
  "find-bar/one-match": { replacement: "FindBar", epic: "F3" },
  // HappeningList draws the history of the holder epic, and an empty history.
  "happening-list/holder": { replacement: "HappeningList", epic: "F4" },
  "happening-list/empty": { replacement: "HappeningList", epic: "F4" },
  // InboxRow draws a line that answered a question with the choice it was given, on the waiting session's row.
  "inbox-row/answered": { replacement: "InboxRow", epic: "F2" },
  // Notice draws a stack of three notices, and of more than three with its "+n more" line.
  "notice/three-notices": { replacement: "Notice", epic: "F5" },
  "notice/over-the-limit": { replacement: "Notice", epic: "F5" },
  // Orrery draws the rota export with its phase cells open to a click.
  "orrery/opens-phase": { replacement: "Orrery", epic: "F4" },
  // TitleRow draws a crumb and a state, a blocker, and a page's commands.
  "title-row/crumbs-and-state": { replacement: "TitleRow", epic: "F3" },
  "title-row/blocker": { replacement: "TitleRow", epic: "F3" },
  "title-row/with-commands": { replacement: "TitleRow", epic: "F3" },
};
