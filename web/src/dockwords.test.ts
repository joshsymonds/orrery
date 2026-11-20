import assert from "node:assert/strict";
import { test } from "node:test";

import {
  ago,
  type DockFacts,
  dockLines,
  elapsed,
  placeholder,
  stateLine,
  trayLabel,
  trayWhy,
  waitsLine,
} from "./dockwords";
import { clock } from "./reader";

// The dock's words, over fixed instants: every clock reading is computed from `now`, never the wall clock. Each test's
// name starts with its group token: DockState, DockQueue, DockWaits or DockLines.

const now = 100_000;

function facts(over: Partial<DockFacts> = {}): DockFacts {
  return {
    kind: "session",
    name: "widgets",
    state: "",
    interrupting: false,
    interruptingSince: 0,
    turnSince: 0,
    tool: null,
    changed: 0,
    reason: "",
    interruptedAt: 0,
    putOff: false,
    step: null,
    ...over,
  };
}

test("DockState: a held step says held, with no clock or control", () => {
  const line = stateLine(facts({ kind: "step", state: "working", step: { label: "deploy", held: true, doing: "" } }), now);
  assert.deepEqual(line, { tone: "held", word: "held", tool: "", what: "", clock: "", control: "none" });
});

test("DockState: a held step's line says held with its doing in the what, and no clock or control", () => {
  const line = stateLine(
    facts({ kind: "step", state: "working", step: { label: "deploy", held: true, doing: "waits on your answer" } }),
    now,
  );
  assert.deepEqual(line, {
    tone: "held",
    word: "held",
    tool: "",
    what: "waits on your answer",
    clock: "",
    control: "none",
  });
});

test("DockState: a step not held says running, its tool, and the turn's elapsed time, even while interrupting", () => {
  const line = stateLine(
    facts({
      kind: "step",
      state: "working",
      step: { label: "deploy", held: false, doing: "" },
      tool: { name: "mcp__orrery__step_start", arg: "deploy", start: now - 30 },
      turnSince: now - 125,
      interrupting: true,
      interruptingSince: now - 5,
    }),
    now,
  );
  assert.deepEqual(line, {
    tone: "working",
    word: "running",
    tool: "step_start",
    what: "deploy",
    clock: "2:05",
    control: "interrupt",
  });
});

test("DockState: a step that is not held with no tool says thinking, with no clock when the turn has no start", () => {
  const line = stateLine(facts({ kind: "step", step: { label: "deploy", held: false, doing: "" } }), now);
  assert.deepEqual(line, { tone: "working", word: "running", tool: "", what: "thinking", clock: "", control: "interrupt" });
});

test("DockState: interrupting while working says interrupting…, with the wait since the interrupt began and no tool", () => {
  const line = stateLine(
    facts({
      state: "working",
      interrupting: true,
      interruptingSince: now - 75,
      tool: { name: "Bash", arg: "go test", start: now - 20 },
    }),
    now,
  );
  assert.deepEqual(line, {
    tone: "interrupting",
    word: "interrupting…",
    tool: "",
    what: "waiting for the turn to end",
    clock: "1:15",
    control: "interrupting",
  });
});

test("DockState: interrupting while working with no interrupt time says interrupting… with no clock", () => {
  const line = stateLine(facts({ state: "working", interrupting: true }), now);
  assert.equal(line.word, "interrupting…");
  assert.equal(line.clock, "");
});

test("DockState: working shows the tool's short name and its arg, and the turn's elapsed time first", () => {
  const line = stateLine(
    facts({
      state: "working",
      tool: { name: "mcp__orrery__step_start", arg: "go test", start: now - 20 },
      turnSince: now - 200,
      changed: now - 400,
    }),
    now,
  );
  assert.deepEqual(line, {
    tone: "working",
    word: "working",
    tool: "step_start",
    what: "go test",
    clock: "3:20",
    control: "interrupt",
  });
});

test("DockState: working with no turn start falls back to the tool's start for its clock", () => {
  const line = stateLine(
    facts({ state: "working", tool: { name: "Bash", arg: "", start: now - 20 }, changed: now - 400 }),
    now,
  );
  assert.equal(line.what, "thinking");
  assert.equal(line.clock, "0:20");
});

test("DockState: working with no turn start or tool start falls back to the state's change for its clock", () => {
  const line = stateLine(facts({ state: "working", changed: now - 61 }), now);
  assert.equal(line.tool, "");
  assert.equal(line.clock, "1:01");
});

test("DockState: working with no start time at all has no clock", () => {
  const line = stateLine(facts({ state: "working" }), now);
  assert.equal(line.clock, "");
});

test("DockState: waiting on a question you put off says so, with no clock or control", () => {
  const line = stateLine(facts({ state: "waiting", putOff: true }), now);
  assert.deepEqual(line, {
    tone: "idle",
    word: "waiting on a question you put off",
    tool: "",
    what: "",
    clock: "",
    control: "none",
  });
});

test("DockState: waiting says waiting on you, with no clock or control", () => {
  const line = stateLine(facts({ state: "waiting" }), now);
  assert.deepEqual(line, { tone: "waiting", word: "waiting on you", tool: "", what: "", clock: "", control: "none" });
});

test("DockState: done after your interrupt says interrupted by you, with the time of day it was interrupted", () => {
  const interruptedAt = now - 600;
  const line = stateLine(facts({ state: "done", interruptedAt, changed: now - 60 }), now);
  assert.deepEqual(line, {
    tone: "interrupted",
    word: "interrupted by you",
    tool: "",
    what: `· ${clock(BigInt(interruptedAt))}`,
    clock: "",
    control: "none",
  });
});

test("DockState: done says how long ago it changed", () => {
  const line = stateLine(facts({ state: "done", changed: now - 120 }), now);
  assert.deepEqual(line, { tone: "done", word: "done", tool: "", what: "· 2m ago", clock: "", control: "none" });
});

test("DockState: done with no change time says nothing more", () => {
  const line = stateLine(facts({ state: "done" }), now);
  assert.deepEqual(line, { tone: "done", word: "done", tool: "", what: "", clock: "", control: "none" });
});

test("DockState: interrupted says cut off, with its reason and age, and offers resume", () => {
  const line = stateLine(facts({ state: "interrupted", reason: "turn cancelled", changed: now - 7_200 }), now);
  assert.deepEqual(line, {
    tone: "interrupted",
    word: "cut off",
    tool: "",
    what: "· turn cancelled · 2h ago",
    clock: "",
    control: "resume",
  });
});

test("DockState: interrupted drops an empty reason from its parts", () => {
  const line = stateLine(facts({ state: "interrupted", changed: now - 240 }), now);
  assert.equal(line.what, "· 4m ago");
});

test("DockState: failed says failed, with its reason and age, and no control", () => {
  const line = stateLine(facts({ state: "failed", reason: "quota exceeded", changed: now - 240 }), now);
  assert.deepEqual(line, {
    tone: "failed",
    word: "failed",
    tool: "",
    what: "· quota exceeded · 4m ago",
    clock: "",
    control: "none",
  });
});

test("DockState: stopped says stopped, with its age in days, and no control", () => {
  const line = stateLine(facts({ state: "stopped", changed: now - 86_400 }), now);
  assert.deepEqual(line, { tone: "stopped", word: "stopped", tool: "", what: "· 1d ago", clock: "", control: "none" });
});

test("DockState: any other state, and a shell, says idle with nothing else", () => {
  assert.deepEqual(stateLine(facts({ state: "" }), now), {
    tone: "idle",
    word: "idle",
    tool: "",
    what: "",
    clock: "",
    control: "none",
  });
  assert.deepEqual(stateLine(facts({ kind: "shell", state: "idle" }), now), {
    tone: "idle",
    word: "idle",
    tool: "",
    what: "",
    clock: "",
    control: "none",
  });
});

test("DockState: placeholder for a shell is a command prompt", () => {
  assert.equal(placeholder(facts({ kind: "shell", state: "idle" }), false), "Type a command…");
});

test("DockState: placeholder for a held step says it resumes with your words", () => {
  const text = placeholder(facts({ kind: "step", step: { label: "deploy", held: true, doing: "" } }), false);
  assert.equal(text, "Nudge deploy. It resumes with your words");
});

test("DockState: placeholder for a running step says it interrupts the turn first", () => {
  const text = placeholder(facts({ kind: "step", step: { label: "deploy", held: false, doing: "" } }), false);
  assert.equal(text, "Nudge deploy. It interrupts the turn, then gives it your words");
});

test("DockState: placeholder while interrupting says stopping", () => {
  const text = placeholder(facts({ state: "working", interrupting: true }), false);
  assert.equal(text, "Stopping widgets…");
});

test("DockState: placeholder while working says a message takes its next tool result", () => {
  assert.equal(
    placeholder(facts({ state: "working" }), false),
    "Queue a message for widgets. Claude takes it at its next tool result",
  );
});

test("DockState: placeholder while working in a pane says Claude Code takes it when it can", () => {
  const text = placeholder(facts({ kind: "pane", state: "working" }), false);
  assert.equal(text, "Queue a message for widgets. Claude Code takes it when it can");
});

test("DockState: placeholder while working and narrow is the short queue prompt, in a pane too", () => {
  assert.equal(placeholder(facts({ state: "working" }), true), "Queue for widgets");
  assert.equal(placeholder(facts({ kind: "pane", state: "working" }), true), "Queue for widgets");
});

test("DockState: placeholder while waiting says answer, and on a question you put off says message", () => {
  assert.equal(placeholder(facts({ state: "waiting" }), false), "Answer widgets");
  assert.equal(placeholder(facts({ state: "waiting", putOff: true }), false), "Message widgets");
});

test("DockState: placeholder after your interrupt says it stopped where it was", () => {
  const text = placeholder(facts({ state: "done", interruptedAt: now - 600 }), false);
  assert.equal(text, "widgets stopped where it was. Say what to do next");
});

test("DockState: placeholder for a cut off session says resume it or say what to do next", () => {
  const text = placeholder(facts({ state: "interrupted" }), false);
  assert.equal(text, "widgets was cut off. Resume it, or say what to do next");
});

test("DockState: placeholder for a failed session says a message starts it again", () => {
  assert.equal(placeholder(facts({ state: "failed" }), false), "widgets failed. A message starts it again");
});

test("DockState: placeholder for a stopped session says a message starts it again", () => {
  assert.equal(placeholder(facts({ state: "stopped" }), false), "widgets is stopped. A message starts it again");
});

test("DockState: placeholder otherwise says message", () => {
  assert.equal(placeholder(facts({ state: "done" }), false), "Message widgets");
});

test("DockState: ago says just now under a minute", () => {
  assert.equal(ago(now - 59, now), "just now");
});

test("DockState: ago says the largest whole unit, in minutes, hours and days", () => {
  assert.equal(ago(now - 240, now), "4m ago");
  assert.equal(ago(now - (2 * 3_600 + 1_800), now), "2h ago");
  assert.equal(ago(now - 86_400, now), "1d ago");
});

test("DockState: elapsed is m:ss, and h:mm:ss from an hour, never negative", () => {
  assert.equal(elapsed(0), "0:00");
  assert.equal(elapsed(59), "0:59");
  assert.equal(elapsed(605), "10:05");
  assert.equal(elapsed(3_600), "1:00:00");
  assert.equal(elapsed(3_725), "1:02:05");
  assert.equal(elapsed(-5), "0:00");
});

test("DockQueue: trayLabel counts every drawn row", () => {
  assert.equal(trayLabel(1), "Queued 1");
  assert.equal(trayLabel(3), "Queued 3");
});

test("DockQueue: a pane with a row not cancelled says Claude Code takes them when it chooses", () => {
  assert.equal(
    trayWhy("pane", [
      { waits: "", cancelled: false },
      { waits: "", cancelled: true },
    ]),
    "Claude Code takes them when it chooses",
  );
});

test("DockQueue: a pane whose rows are all cancelled says dropped when the turn was cancelled", () => {
  assert.equal(trayWhy("pane", [{ waits: "", cancelled: true }]), "dropped when the turn was cancelled");
});

test("DockQueue: a session whose rows are all cancelled says dropped when the session ended", () => {
  assert.equal(trayWhy("session", [{ waits: "start", cancelled: true }]), "dropped when the session ended");
});

test("DockQueue: a session with one start row says Claude takes it as its next turn, cancelled starts uncounted", () => {
  const rows = [
    { waits: "start", cancelled: false },
    { waits: "start", cancelled: true },
  ];
  assert.equal(trayWhy("session", rows), "Claude takes it as its next turn");
});

test("DockQueue: a session with several start rows says Claude takes them as its next turns", () => {
  const rows = [
    { waits: "start", cancelled: false },
    { waits: "start", cancelled: false },
  ];
  assert.equal(trayWhy("session", rows), "Claude takes them as its next turns");
});

test("DockQueue: a session with no start row says Claude takes them at its next tool result", () => {
  assert.equal(trayWhy("session", [{ waits: "turn", cancelled: false }]), "Claude takes them at its next tool result");
});

test("DockWaits: one speak event says it waits until you speak", () => {
  assert.equal(waitsLine([{ waits: "speak" }], false), "1 event waits until you speak");
});

test("DockWaits: several speak events say they wait until you speak", () => {
  assert.equal(waitsLine([{ waits: "speak" }, { waits: "speak" }], false), "2 events wait until you speak");
});

test("DockWaits: a request counts only when a question was put off", () => {
  assert.equal(waitsLine([{ waits: "request" }], false), "");
  assert.equal(waitsLine([{ waits: "request" }], true), "1 event waits on the question you put off");
  assert.equal(
    waitsLine([{ waits: "request" }, { waits: "request" }], true),
    "2 events wait on the question you put off",
  );
});

test("DockWaits: a free event is never counted", () => {
  assert.equal(waitsLine([{ waits: "free" }, { waits: "free" }], true), "");
});

test("DockWaits: speak is said before a request, when both wait", () => {
  assert.equal(waitsLine([{ waits: "request" }, { waits: "speak" }], true), "1 event waits until you speak");
});

test("DockWaits: nothing pending says nothing", () => {
  assert.equal(waitsLine([], true), "");
});

test("DockLines: the send and take-back lines are exact", () => {
  assert.equal(dockLines.sendRefused, "Not sent. Your draft is kept; send again.");
  assert.equal(
    dockLines.sendUnknown,
    "No answer from the server. It may have been sent: check the conversation before sending again.",
  );
  assert.equal(dockLines.alreadyTook, "Claude already took it in.");
  assert.equal(dockLines.takeBackFailed, "Could not take it back.");
  assert.equal(dockLines.takeAllBackFailed, "Could not take them back.");
  assert.equal(dockLines.notDelivered, "not delivered");
});

test("DockLines: backInBox and sendNowHeld say one message or several", () => {
  assert.equal(dockLines.backInBox(1), "Your queued message is back in the box.");
  assert.equal(dockLines.backInBox(3), "Your 3 queued messages are back in the box.");
  assert.equal(dockLines.sendNowHeld(1), "1 message was not sent again. It is back in the box.");
  assert.equal(dockLines.sendNowHeld(2), "2 messages were not sent again. They are back in the box.");
});

test("DockLines: resume lines name the refusal and the unknown outcome", () => {
  assert.equal(dockLines.resumeRefused("quota exceeded"), "Not resumed: quota exceeded");
  assert.equal(dockLines.resumeUnknown, "No answer from the server. It may have resumed: check before trying again.");
});
