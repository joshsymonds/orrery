# orrery flows (round 6, Nov 17 2026)

Nine pages, one frame, three clients. Every page has the same title row: where it is (directory, then the page, the
largest text in the row), then what can be done here. At the desk the inbox is a column beside the page; it hides once a
page is open, pops out over the page, and pins. Under 760 px and on Android there is one page at a time: the inbox is
the home page, a tap goes one page forward and Back returns.

| page | what it answers | scenarios |
|---|---|---|
| Inbox (desk) / Home (phone) | What is running where, and what waits on me? | `inbox-*` |
| Project | What is working in this directory? | `project` |
| Conversation | What did it say, what is it doing, and what have I queued? | `convo-*`, `dock-*`, `ask-*` |
| Epic | What is this epic, where is it, what is it doing, what does it ask of me? | `epic-*` |
| Contract | What did I agree to, or what am I about to agree to? | `contract-*` |
| Phase | Is this task doing what was planned? | `phase-*` |
| Worker session | What exactly did the worker do and say? | `worker-*` |
| Talk | The one conversation that steers this epic, and moving it to another. | `epic-handover`, `epic-talk`, `epic-moved`, `epic-convo-ended` |
| New | Start a session, or find one. | `new-*` |

Every page is a pair, and the words never move. The middle is a conversation; the right is its rail: a tile for the
contract it wrote, each epic it steers, its sub-agents and what it left running. A tile
opens its own panel beside the conversation. An epic, a contract or a task is always read in a panel with its
conversation in the middle.

Android alone: Settings, Pools, system notifications, quick keys (`android-*`). The web alone: the key list (`keys`).

## 1. Seeing everything

- Starts: the desk at load; the phone's home page.
- The inbox is projects. A project is a card: its head is its name and its path; under it hang its conversations,
  and under each conversation hang the epics it steers (with their running steps) and the contract it wrote. Then one
  line for what has ended. An epic is drawn once, under its one conversation. The head opens the project's page: its
  conversations with what each last said, and under each, its epics as live cards.
- Two chips at the top say how many things wait on you and how many rows are new. A chip narrows the tree to those.
  `w` and `n` step through them.
- A row's glyph is its state; an unseen row has a dot and a bold title. An epic's row is a gauge, its name, its count,
  its state in words, and what blocks it.
- Search opens from the head or `/`. Archived is at the foot.
- At rest the inbox is a rail: a tile per project with a count of what waits on you there and dots for working and
  new, then New, search, notices and keys.
- The bell, in the inbox's head and on its rail, opens every notice by day.

## 2. Reading and answering a session

- Starts: a row; a notification; New.
- The transcript is four shapes. A Block is someone speaking: you, the engine, a step that ended, an epic. A Line is
  machinery: a tool call, a run of them, a thought, a notice. A Rule marks a day, a compaction, an interrupt, what is
  new. The reply is plain text with nothing around it.
- A question is the Ask at the foot. One press answers. Not now takes it off your list and sends nothing; Decline,
  in its More, denies it and ends the turn.
- The dock is where you type and where the state is. Its first line says working, with the tool and a clock, and has
  Interrupt. It holds the state, your queue and the input, and nothing else.
- Events for the session (an epic's question, a step that ended) go in at the next turn's end, unseen. After your
  interrupt they wait until you speak, and the dock says how many.
- Its goal heads the band under the title row, then one line for each thing it steers that asks you, in words. What it has started is on its right rail, a tile
  each: an epic's gauge, sub-agents with a count, what was left running. A tile opens its
  own panel.
- Find opens from the title row or `/` in an empty box.
- On the phone the header folds to one line of badges while you read back and is gone while you type; a chevron at
  the end of its tiles, or on the folded line, hides it for good. Folded, it turns peach and names what asks.

## 3. Sending while it works, and stopping it

- A message sent while a turn runs joins the queue on the dock. The tray says when it goes: Claude takes it at the
  running turn's next tool result.
- Any queued message can be taken back into the box. Send now interrupts the turn and the first queued message runs
  next.
- Interrupt (`esc`) takes the queue back, then stops the turn. The dock reads `interrupting…` with a clock, then
  `interrupted by you` with the time, and the transcript ends on the rule.
- `cut off` is different: the server restarted mid-turn. Resume carries on.

## 4. What an epic is doing

- Starts: an epic's row; its tile on the rail of the conversation that steers it; a notification.
- Top to bottom: the summary and its two doors (Contract, Conversation); the track; Now; Just happened; Tasks;
  Requirements; then Review, Release and Report as it reaches them.
- A question or a park is the Ask under Now: pick, then confirm, with the line that will be recorded.
- Pause, Resume and Stop are in the title row at the desk and in a bar at the foot on the phone. Stop asks first.

## 5. Drilling into a task

- Starts: a wedge of the chart, a row of the task table, a running step, a requirement.
- Task page: first a band that ties it to its epic: the epic's line, every task of that epic with this one marked.
  Then the requirement it proves and the planner's brief, then its five stages in order (red, implement, green,
  review, integrate), the one it is in opened. The epic's conversation stays in the middle. Checking shows each round as a block, the
  gate's last lines, the judge.
- Open the session: the worker's conversation. Its dock nudges: a message interrupts the turn and then gives it your
  words. Interrupt holds the step; Stop is behind More and asks first.
- Back goes up one crumb; any crumb jumps there.

## 6. Guiding an epic with words

- An epic is steered from exactly one conversation. That conversation is in the middle, input ready, and the epic is
  the record on the right. One conversation may steer several epics; its band shows three and folds the rest.
- Moving it: the record's "Steer it from another conversation" opens "Steer rota-export from": the one that steers it now, the project's other
  conversations, and a new one that starts with the contract and the epic's log. Or ask another conversation to take
  it over, and it does so with a tool call. The move is a line on the epic's log and a line in each conversation; from
  then on its questions go to the new one.
- Another conversation can read an epic and talk about it; it changes it only by taking it over. Answering, pausing
  or stopping from the epic's page is you, and goes to its conversation as an event.
- If its conversation ends, the conversation says so where its dock was, with Continue in a new conversation and Steer
  from another. An epic started from a shell gets a conversation made for it.
- Under 1250 the panel lies over the conversation from the right edge; Close returns. On the phone the bar at the epic's foot opens
  the conversation as the next page.

## 7. A contract

- It is written by a session: the Director, running Gambit's brainstorm, which presents it for acceptance. While it
  waits, it is a tile on that conversation's rail, and one line in the transcript where it was written.
- It is Gambit's document: a decision line, then ten sections in its order.
- Its page is sections of rows. What is yours to settle is marked in attention. It is read on the right with the
  conversation that wrote it in the middle; a row's control puts that row on the message as what the message is about. On the phone that conversation is a
  sheet over the lower half, so the contract stays in view.
- It is a record from the moment it is proposed. Each change is a version with its time, its writer and what changed,
  and the rows a version changed carry its mark. It is proposed, accepted, or set aside; set aside it keeps its versions
  and the words it was set aside with, and Reopen proposes it again.
- It is found under its conversation in the inbox and on its project's page, on its conversation's rail while it waits, and from its epic once accepted.
- While it waits, the band under its conversation's title row says what is left to settle, with Read and decide;
  while the contract is open, that line says it is open on the right.
- Accept opens when nothing is left, and freezes the version accepted. After acceptance the epic's page links to the
  contract.

## 8. Starting something

- New is in the inbox's head, and `N` or `⌘K` anywhere.
- One field. Results say what choosing each does: go to, resume, start in.
- Choosing a directory opens the start form: where, and the first message.

## Moving between a conversation and what it made

- The words never move. A tile on the conversation's rail opens its thing in a panel between the conversation and the
  rail; another tile switches it, and the lit tile or Close shuts it. From the inbox or a project the same record opens with its conversation in the
  middle. Nothing opens with no way to talk about it.
- The words follow the work down: from an epic into a task, the task replaces the epic in the panel, with
  rota-export / export-cli in its crumbs, and the epic stays in sight as the band at its top.
- Talking about a thing is a message with that thing on it.
- Both rails are always there: the inbox on the left, what the conversation steers on the right. Hovering the left
  rail, or `b`, brings the inbox out over the page; `B` pins it. On the right, a tile opens its panel, and `]` shuts
  the open one or reopens the last.

## Where a notification lands

- A session asks: that session, at its Ask.
- An epic asks, or a release step failed: the epic's page at Now.
- A contract is ready: its conversation, at the contract.
- Released, ended with gaps: the epic's page at the Report.
- A usage limit, an alert: nowhere; it is read where it is.
