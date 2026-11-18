# Design system: proposed 2026-11-17, a draft waiting on the owner

Status: **draft.** These are the choices SYSTEM.md and tokens.json make, each with what it answers (ids are from
AUDIT.md) and what was rejected. None has been drawn yet, so each is a position to argue with, not a result. Standing
decisions are kept: Catppuccin Mocha, all mono on the web, a title is a directory and a summary, no ids or hashes, and
round 5's six asks.

## 1. Four inks, and the lowest one that carries information is 5.8:1

Answers: A1, B3, D (79 of 167 grey text rules are under 4.5:1; three more dim with opacity).

`ink.1` text, `ink.2` subtext0, `ink.3` overlay2, `ink.4` overlay1. Overlay0 stops being a text colour. Subtext1 goes.
Nothing is dimmed with opacity; "pending" and "not reached" get a dashed rail or a word.

Rejected:
- Keeping overlay1 as the meta colour. It is 4.4:1 on the page and 3.4:1 on a raised surface, and meta text is the
  smallest text.
- A fifth grey. Five was how subtext1 and overlay0 ended up picked at random.
- Brightening Mocha. The palette is the owner's choice; overlay2 is already in it.

## 2. Five type sizes: 11, 12, 14, 16, 22

Answers: A1, B2.1 (the page title is 12 px; three pages have three title sizes).

Rejected:
- Keeping 13 for list rows and controls. 12, 13 and 14 in a mono face differ by weight and colour, not by size; a
  step nobody can see is not a level. Rows take 14 for the title and 12 for everything else. This costs about two
  characters of summary per row at 340 wide.
- Round 5's 13.5 and 15. Half pixels were the start of the 19.
- A proportional face for replies (round 4's Q2). The owner has since decided all mono.

## 3. Colour is named by role, and peach means "it waits on you"

Answers: A5 (a rail means four things), A6 (three state vocabularies), B3 (success is the loudest thing).

Thirteen roles over nine hues; one state table for the glyph, the pill, the composer and the phone. Success is quiet:
a tool that worked has no tick, and its dot in a run is grey; only a failure or a running call is coloured.

Rejected:
- A green tick per successful tool (round 5 drew this). Fifty ticks say nothing and outshine the reply.
- Red for a stopped session. The owner stopped it; red is for what failed.
- A second accent for the assistant's voice. Round 5 rejected it and the reason holds.

## 4. Four shapes in a conversation: Block, Line, Rule, Ask

Answers: A5 (seven block looks, two tool looks), B3.

Every item kind is one of four shapes hung from the column's left edge. The rail's tone is the verdict when the block
has one, else whether it asks, else who spoke. The step-end card, the epic's event line, the epic card, the check card
and the generic event prompt become variants of one Block. "Agent finished" becomes a Line, the same line its
background task had while it ran.

Rejected:
- Keeping the event ring as a pill-shaped row. It is the only such shape in the app and exists because it was built
  alone.
- Rail colour as speaker only. A worker session is all engine; every rail would be teal and the red-then-green of a
  failed and a passed round, the most useful thing round 5 drew there, would go.
- Boxes around tool rows (Android today, round 4). Round 5 rejected them for the web; the phone should follow.

## 5. The transcript is the past; the dock is the pending

Answers: B3 (waiting cards and background rows sit after live text, dimmed to about 2:1, and scroll out of sight), C2
(the phone draws the queue in the transcript, against Q6).

Running background tasks, held events ("arrives when you next speak", "arrives when the session is free") and queued
messages are three trays on the composer, in that order. When a task ends it leaves the tray and its "finished" line
is in the transcript. When an event arrives it becomes a block in the transcript.

Rejected:
- Leaving them in the scroll, as shipped. They are state, not history; drawn as dimmed history they are the least
  legible things in the reader, and they are invisible once the owner scrolls up to read.
- A separate panel beside the reader. It costs width the reading column needs at 1000.

This moves three things that only just shipped. It is open item 1.

## 6. One Ask, and it is the only loud thing on a page

Answers: A5 (four question looks on the web, three on Android), B3 (the whole question in bold 15 px), C3.

One component in the inbox row, the conversation, the epic page and the phone: who asks, the question at reading size
in regular weight, numbered options with peach key caps, the recommended one marked. A session's question answers on
one press; an epic's is pick, then confirm (round 4's P5).

Rejected:
- Bold for the question. A paragraph of bold is harder to read than the reply above it. The card's rail, tint and
  caps already say it is the question.
- "CLAUDE IS ASKING" (Android). A step, an epic or a planner can ask.
- Green for "recommended". It is the asker's opinion, not a verdict.

## 7. State is drawn once, the title is the largest text in its row

Answers: B2.1, B2.2, B2.5, B2.6.

The title row is two lines: crumbs with the pill and commands, then the strip. The state word goes; the pill is the
state. A blocker is a second pill in peach, cut with an ellipsis, and the now block says it in full once.

## 8. Meters keep the phone's colours and thresholds

Answers: B2.3, C3. Both clients already agree on the level and band rules, so the tokens record them unchanged. The
strip's stat becomes label, a short bar, value on one line at the desk (round 5's drawing), and stays label over value
over bar on the phone.

## 9. Three radii, eight spaces, one rail width

Answers: A2. 4, 8, 12. A rail is 3 px everywhere, including the running call and the composer's edge (both 2 today).
Round 5's 3/10 corners on a rail block become square on the rail side and 8 on the other.

## 10. Phone width is a drill, not a stack

Answers: B8. Under 760 the web shows one page at a time, as the Android app does, and the 412 mockups are meant to be
the reference for both.

## Open for the owner

1. **The dock (decision 5).** Move running tasks, arriving events and the queue onto the composer, or leave tasks and
   waiting cards in the transcript and only fix their look. Recommended: the dock. Risk: a busy session's dock is
   tall; each tray is capped at three rows.
2. **List rows at 14 px.** Recommended: yes, for a five-step scale. The alternative is a sixth size (13) used only by
   the inbox.
3. **Peach in meters.** A peach CPU bar and a peach context band break "peach waits on you". Recommended: leave them,
   because you asked for the strip to match the phone and both clients agree today; revisit if the strip keeps
   pulling the eye. The alternative is warn in yellow and three context bands.
4. **Stopped is grey, not red.** Recommended: grey.
5. **A step that waits on a limit** is drawn like one held for you (peach) because `blockerOf` returns both as
   `held`. Recommended: peach only when it is yours to release; a limit wait in grey with `‖`. Needs the two told
   apart in the blocker.
6. **Ended epics in the inbox.** Recommended: at most three unseen ones under Epics, the rest behind "n ended
   earlier"; plain words for outcomes; no repository hash.
7. **Android follows in full.** Round 5 left the phone alone (its open item 3). Recommended: yes. The title becomes
   directory and summary, the list takes the web's attention order, tool rows lose their boxes, and the seven missing
   item kinds are drawn.
8. **The goal line on the web.** Android draws the session's goal; the web has the field and draws nothing.
   Recommended: a line in the dock, above the trays.
9. **A visible Interrupt on the web.** Today it is Esc in the composer and is not in the key list. Recommended: a
   Quiet button in the dock while working, and a line in the key list.
10. **Round 5's contract card and inbox hide-on-open** were not seen in the live build. Not yet checked whether they
    were built and are off, or were never built.
