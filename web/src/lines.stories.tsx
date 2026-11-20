// The stories of the tool lines: ToolLine draws one call, and ToolRun three or more in a row, in round 6's states. A
// state that opens holds a reveal of the call it opens, which the reader gives a call the find brings into view.

import { create } from "@bufbuild/protobuf";

import type { Stories } from "./catalog/story";
import { type ConversationItem, ConversationItemSchema } from "./gen/orrery/v1/orrery_pb";
import { type CallReveal, ToolLine as Line, ToolRun as Run, type RunCall } from "./lines";

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

export const ToolLine: Stories = {
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
      item={callItem("Bash", "go vet ./...", "v1")}
      result={resultItem("v1", "vet: unused variable", true)}
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
};

export const ToolRun: Stories = {
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
        runCall("c", "Bash", "go test ./internal/roster/...", undefined, true),
      ]}
      query=""
    />
  ),
};
