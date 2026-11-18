# The owner's notes on the live web client, 2026-11-17

In his words, lightly ordered. Look at the live page yourself: on Vermissian in Firefox, or through the read-only proxy at http://127.0.0.1:8599/.

## The inbox
- Right now the web UI is very confused and unusable. At the top are questions from an old agent that he wants to dismiss, a random parked notification below that, then epics underneath.
- It should be an inbox on the left, sort of like Claude.ai's or ChatGPT's sites, organized by the directory where the agent is working, with epics listed underneath the agents.
- Filtering and organizing at the top is confusing and misplaced on the inbox.
- The triple dots next to conversations in the inbox, the number next to Orrery, and the back and forward carets are unclear.

## Epics
- The epic views are unhelpful. As much as he likes the orrery view, it is very difficult to understand and use in the inbox, and even in epics.
- Epics are colorless and undifferentiated.
- Clicking on them is ANTI-helpful: huge blocks of text that seem to have nothing to do with anything.

## Chat
- Chat is okay, except "working" sits in the upper right instead of over the conversation. Android is much more usable here.
- Ask is not clear about what it does. It should probably be New or something similar.
- The conversation model still seems wrong; possibly a UI/UX problem:
  - A message stays queued, and he cannot tell what it is waiting behind.
  - The queue is hard to reason about: when will it send, and how does he edit it?
  - Interrupts: after interrupting, how does he know it has stopped?
  - (Diagnosis, 2026-11-17: an owner message to a working headless session is held until the turn ends, internal/enginehost/manager.go Say and idleHeldLocked. A long turn holds it indefinitely; Claude Code's own TUI delivers at the next tool call.)

## Everywhere
- The machine meters at the top (net, CPU, RAM and so on) are confusing and undifferentiated.
- Everything is too small, and what each control does is vaguely unclear.

## Added later the same day
- An epic created in a conversation becomes a card where the create call was, so it sits stuck in the middle of the page and scrolls away. Its view is not very helpful either. (Round 5 rejected putting the decision form in the card for this reason and proposed a small line to the epic in the title row's strip; check whether that was built.)
- Pending step-end events draw as waiting cards whose summary is one line of raw JSON (6,500+ characters), so two enormous cards sat at the bottom of the conversation.
- Background shell tasks (a server left running, a timer) draw as "live" rows indistinguishable from work in progress.
