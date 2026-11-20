// The stories of Dock: the box under a reader, with its state line, its waits line, its tray and its about line. Each state
// draws the facts it names, the tray or the lines its props name. Its waiting messages are the fixture pane's, and its
// session messages are built as a stream holds them. A step's box is the Dock with a step's facts and calls.

import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { useEffect, useRef } from "preact/hooks";

import type { Stories } from "./catalog/story";
import { applyDelta } from "./conversation";
import * as dock from "./dock";
import type { DockFacts } from "./dockwords";
import { liveEpoch } from "./fixtures/clock";
import { paneStreams } from "./fixtures/panes";
import { PendingEventSchema, QueuedMessageSchema } from "./gen/orrery/v1/orrery_pb";

// must returns value, or throws naming what the fixtures do not hold.
function must<T>(value: T | undefined, what: string): T {
  if (value === undefined) {
    throw new Error(`fixtures: no ${what}`);
  }
  return value;
}

const now = Date.parse(liveEpoch) / 1000;

// The pane's waiting messages, as its conversation holds them, and the same messages with the second canceled.
const waiting = applyDelta(null, must(paneStreams.get("%11"), "lamp stream")).state?.waiting ?? [];

// queuedMessage is a session's message as its stream holds it, in the state the session gives it.
const queuedMessage = (promptId: string, text: string, state: string) =>
  create(QueuedMessageSchema, { promptId, text, state });

// facts is a session named lighthouse, with every fact at its zero value but the ones a story names.
const facts = (init: Partial<DockFacts>): DockFacts => ({
  kind: "session",
  name: "lighthouse",
  state: "done",
  interrupting: false,
  interruptingSince: 0,
  turnSince: 0,
  tool: null,
  changed: 0,
  reason: "",
  interruptedAt: 0,
  putOff: false,
  step: null,
  ...init,
});

const base = {
  now,
  send: () => Promise.resolve(),
  pull: null,
  newPromptId: () => "prompt-story",
};

// stepFacts is a running or held step named label, which its turn started at now minus 83 seconds.
const stepFacts = (label: string, held: boolean): DockFacts =>
  facts({
    kind: "step",
    name: label,
    state: "working",
    turnSince: now - 83,
    step: { label, held, doing: held ? "waits on your answer" : "" },
  });

// stepCalls are a step's calls in the stories, which resolve at once: the fixture's Steps absorbs them.
const stepCalls = { hold: () => Promise.resolve(), stop: () => Promise.resolve() };

// StepConfirm is a running step whose More is pressed as it draws, so the story draws the question Stop {label}?.
function StepConfirm() {
  const root = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    root.current?.querySelector<HTMLButtonElement>('button[aria-label="Stop this step…"]')?.click();
  }, []);
  return (
    <div ref={root}>
      <dock.Dock {...base} facts={stepFacts("T2", false)} stepCalls={stepCalls} />
    </div>
  );
}

// AboutDraft is a waiting session whose message is about an alert, with the draft round 6 draws typed in its box.
function AboutDraft() {
  const field = useRef<HTMLTextAreaElement | null>(null);
  useEffect(() => {
    const box = field.current;
    if (box === null) {
      return;
    }
    box.value = "Make it one alert per table.";
    box.dispatchEvent(new Event("input", { bubbles: true }));
  }, []);
  return (
    <dock.Dock
      {...base}
      facts={facts({ state: "waiting" })}
      about="F2 The harbour office gets the same alert many times."
      inputRef={field}
    />
  );
}

// refusedSend is the server's refusal of a send, as a session that is busy gives it.
const refusedSend = () => Promise.reject(new ConnectError("session is busy", Code.FailedPrecondition));

// SendRefused is a dock whose send the server refuses. It types a draft and sends it once it has drawn, so the story
// draws the refusal the way a refused send does.
function SendRefused() {
  const field = useRef<HTMLTextAreaElement | null>(null);
  useEffect(() => {
    const box = field.current;
    if (box === null) {
      return;
    }
    box.value = "Also check the spool directory.";
    box.dispatchEvent(new Event("input", { bubbles: true }));
    // The send reads the draft the input event set, which the next render holds, so Enter follows that render.
    const timer = setTimeout(() => box.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true })), 0);
    return () => clearTimeout(timer);
  }, []);
  return <dock.Dock {...base} facts={facts({ state: "done" })} send={refusedSend} inputRef={field} />;
}

export const Dock: Stories = {
  done: () => <dock.Dock {...base} facts={facts({ state: "done", changed: now - 300 })} />,
  working: () => (
    <dock.Dock
      {...base}
      facts={facts({
        state: "working",
        turnSince: now - 83,
        tool: { name: "mcp__orrery__step_start", arg: "deploy", start: now - 10 },
      })}
      onInterrupt={() => {}}
    />
  ),
  "pane-queue": () => (
    <dock.Dock
      {...base}
      facts={facts({ kind: "pane", name: "lamp", state: "working", turnSince: now - 40 })}
      queued={waiting}
    />
  ),
  waiting: () => <dock.Dock {...base} facts={facts({ state: "waiting" })} />,
  interrupting: () => (
    <dock.Dock
      {...base}
      facts={facts({ state: "working", interrupting: true, interruptingSince: now - 20, turnSince: now - 100 })}
      onInterrupt={() => {}}
    />
  ),
  "interrupted-by-you": () => <dock.Dock {...base} facts={facts({ state: "done", interruptedAt: now - 600 })} />,
  failed: () => (
    <dock.Dock {...base} facts={facts({ state: "failed", reason: "upstream returned 500", changed: now - 120 })} />
  ),
  stopped: () => <dock.Dock {...base} facts={facts({ state: "stopped", changed: now - 7_200 })} />,
  "cut-off": () => (
    <dock.Dock
      {...base}
      facts={facts({ state: "interrupted", reason: "tool timeout", changed: now - 180 })}
      resume={() => Promise.resolve()}
    />
  ),
  "with-queued": () => (
    <dock.Dock
      {...base}
      facts={facts({ state: "working" })}
      sessionQueue={[queuedMessage("prompt-queued", "Also check the spool directory.", "queued")]}
      withdraw={() => Promise.resolve([])}
    />
  ),
  "queued-cancelled": () => (
    <dock.Dock
      {...base}
      facts={facts({ state: "done" })}
      sessionQueue={[queuedMessage("prompt-discarded", "Retry the upload after the spool drains.", "discarded")]}
    />
  ),
  about: () => <AboutDraft />,
  "events-wait": () => (
    <dock.Dock
      {...base}
      facts={facts({ state: "working", turnSince: now - 83 })}
      pending={[
        create(PendingEventSchema, { id: "event-1", kind: "step_end", step: "step-7", waits: "speak" }),
        create(PendingEventSchema, { id: "event-2", kind: "step_end", step: "step-8", waits: "speak" }),
      ]}
      onInterrupt={() => {}}
    />
  ),
  "send-error": () => <SendRefused />,
  focused: () => <dock.Dock {...base} facts={facts({ state: "done" })} inputRef={(element) => element?.focus()} />,
  shell: () => <dock.Dock {...base} facts={facts({ kind: "shell", name: "tidepool", state: "idle" })} />,
  "step-running": () => <dock.Dock {...base} facts={stepFacts("T2", false)} stepCalls={stepCalls} />,
  "step-held": () => <dock.Dock {...base} facts={stepFacts("T1", true)} stepCalls={stepCalls} />,
  "step-confirm": () => <StepConfirm />,
};
