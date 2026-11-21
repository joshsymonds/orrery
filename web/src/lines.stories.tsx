// The stories of the Lines: ToolLine draws one call, and ToolRun three or more in a row; ThinkingLine a thought, NoticeLine
// a notice, and EngineFold a brief or an engine prompt, in round 6's states. Every state draws inside the reader's element,
// where the reader's CSS applies. A tool state that opens holds a reveal of the call it opens, which the reader gives a call
// the find brings into view. A thinking state and a brief that open are clicked open once they are drawn, as the owner would
// open them.

import { create } from "@bufbuild/protobuf";
import type { ComponentChildren } from "preact";
import { useLayoutEffect, useRef } from "preact/hooks";

import { type Stories, inReader } from "./catalog/story";
import { type ConversationItem, ConversationItemSchema } from "./gen/orrery/v1/orrery_pb";
import {
  type CallReveal,
  EngineFold as Fold,
  NoticeLine as Notice,
  ThinkingLine as Thinking,
  ToolLine as Line,
  ToolRun as Run,
  type RunCall,
} from "./lines";

const noop = () => {};

// callItem is a tool call under id, made by tool with input.
function callItem(tool: string, input: string, id: string): ConversationItem {
  return create(ConversationItemSchema, { kind: "tool", tool, input, id });
}

// resultItem is the result of the call under id, failed when error is set.
function resultItem(id: string, text: string, error = false): ConversationItem {
  return create(ConversationItemSchema, { kind: "result", id, text, error });
}

// revealOf is the reveal of the call under id that opens its result.
function revealOf(id: string): CallReveal {
  return { id, done: noop };
}

// runCall is a call of a run under key, with its result when one is given.
function runCall(key: string, tool: string, input: string, result?: ConversationItem, running = false): RunCall {
  return { key, item: callItem(tool, input, key), result, running, current: -1 };
}

export const ToolLine: Stories = inReader({
  done: () => (
    <Line
      item={callItem("Read", "internal/roster/handler.go", "r1")}
      result={resultItem("r1", "ok")}
      running={false}
      query=""
      current={-1}
    />
  ),
  failed: () => (
    <Line
      item={callItem("Bash", "go test ./internal/roster -run Slow -count=1", "b1")}
      result={resultItem("b1", "--- FAIL: TestSlowWeek (4.21s)", true)}
      running={false}
      query=""
      current={-1}
    />
  ),
  open: () => (
    <Line
      item={callItem("Grep", "keeperFor\\(", "g1")}
      result={resultItem("g1", "14 matches in 3 files")}
      running={false}
      query=""
      current={-1}
      reveal={revealOf("g1")}
    />
  ),
  "open-failed": () => (
    <Line
      item={callItem("Bash", "go test ./internal/roster -run Slow -count=1", "v1")}
      result={resultItem("v1", "--- FAIL: TestSlowWeek (4.21s)\n    roster_test.go:88: week of 44 shifts took 4.1s, want under 500ms\nFAIL", true)}
      running={false}
      query=""
      current={-1}
      reveal={revealOf("v1")}
    />
  ),
  running: () => (
    <Line
      item={callItem("Bash", "go test ./internal/telemetry/...", "t1")}
      result={undefined}
      running
      query=""
      current={-1}
    />
  ),
});

export const ToolRun: Stories = inReader({
  closed: () => (
    <Run
      calls={[
        runCall("a", "Read", "internal/roster/query.go", resultItem("a", "ok")),
        runCall("b", "Read", "internal/roster/keeper.go", resultItem("b", "ok")),
        runCall("c", "Edit", "internal/roster/query.go", resultItem("c", "ok")),
        runCall("d", "Bash", "go vet ./...", resultItem("d", "vet: unused variable", true)),
      ]}
      query=""
    />
  ),
  open: () => (
    <Run
      calls={[
        { ...runCall("a", "Read", "internal/roster/query.go", resultItem("a", "ok")), reveal: revealOf("a") },
        runCall("b", "Read", "internal/roster/keeper.go", resultItem("b", "ok")),
        runCall("c", "Edit", "internal/roster/query.go", resultItem("c", "ok")),
        runCall("d", "Bash", "go vet ./...", resultItem("d", "vet: unused variable", true)),
      ]}
      query=""
    />
  ),
  running: () => (
    <Run
      calls={[
        runCall("a", "Read", "internal/roster/query.go", resultItem("a", "ok")),
        runCall("b", "Read", "internal/roster/keeper.go", resultItem("b", "ok")),
        runCall("c", "Read", "internal/roster/roster_test.go", resultItem("c", "ok")),
        runCall("d", "Bash", "go test ./internal/roster/...", undefined, true),
      ]}
      query=""
    />
  ),
});

// Clicked draws its children and clicks the first button it holds once drawn, as the owner opens a line.
function Clicked({ children }: { children: ComponentChildren }) {
  const root = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    root.current?.querySelector<HTMLButtonElement>("button")?.click();
  }, []);
  return <div ref={root}>{children}</div>;
}

export const ThinkingLine: Stories = inReader({
  closed: () => <Thinking text="The handler loads its keeper for each row. Check it before guessing." />,
  open: () => (
    <Clicked>
      <Thinking text="The handler loads its keeper for each row. Check it before guessing." />
    </Clicked>
  ),
});

export const NoticeLine: Stories = inReader({
  default: () => (
    <Notice item={noticeItem("task", "Task 3 of 4 is complete: one query per week instead of one per shift.")} query="" current={-1} />
  ),
  error: () => (
    <Notice item={noticeItem("error", "The API refused the request: overloaded. Retrying in 8 s.", true)} query="" current={-1} />
  ),
});

// promptItem is a prompt sent by sender, in round, with text.
function promptItem(sender: string, text: string, round = 0): ConversationItem {
  return create(ConversationItemSchema, { kind: "prompt", sender, round, text });
}

// noticeItem is a notice of kind with text, failed when error is set.
function noticeItem(kind: string, text: string, error = false): ConversationItem {
  return create(ConversationItemSchema, { kind: "notice", notice: kind, text, error });
}

const briefText = "Implement the week query. Own `internal/roster/query.go` and its test. Do not change `go.mod`.";

export const EngineFold: Stories = inReader({
  "brief-closed": () => <Fold item={promptItem("brief", briefText)} query="" current={-1} />,
  "brief-open": () => (
    <Clicked>
      <Fold item={promptItem("brief", briefText)} query="" current={-1} />
    </Clicked>
  ),
  "engine-closed": () => (
    <Fold
      item={promptItem("engine", "Round 2: the owned-files check failed. Revert go.mod.\nThe other checks passed.")}
      query=""
      current={-1}
    />
  ),
});
