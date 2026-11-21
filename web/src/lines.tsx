// The Lines of a conversation: machinery that hangs on the hairline thread, small and without a rail. A tool call is one
// line, and three or more calls in a row are one run line that opens to a line per call. A line with something to open is
// a button, and what it opens follows it in an opened element.

import { useLayoutEffect, useRef, useState } from "preact/hooks";

import { useFlash } from "./flash";
import type { ConversationItem } from "./gen/orrery/v1/orrery_pb";
import { Icon, type IconName } from "./glyphs";
import { Markdown } from "./markdown";
import { MarkedText, ReplyText } from "./text";

// CallReveal is a request to bring the tool call with id into view and open it. done ends the request once the call's
// row has acted on it, so a row that mounts later, as a closed run opens again, does not act on it a second time.
export interface CallReveal {
  id: string;
  done: () => void;
}

// partCurrent is the offset within a part of an item's searched text where the current match starts, when it starts in
// that part, and -1 when it does not. The part starts at start in the item's searched text, where findMatches joins the
// item's parts one to a line, so the part after a newline starts one past the part before it.
export function partCurrent(current: number, start: number, length: number): number {
  return current >= start && current < start + length ? current - start : -1;
}

// useReveal opens a fold when revealed turns true, which is when a find's current match moves into it. The owner can
// close the fold after that, and it opens again only when revealed turns true once more. The fold opens in a layout
// effect, so the current mark it holds mounts in the render that follows and scrolls itself into view.
export function useReveal(revealed: boolean, setShown: (shown: boolean) => void): void {
  useLayoutEffect(() => {
    if (revealed) {
      setShown(true);
    }
  }, [revealed]);
}

// toolIcons is the icon of each tool kind the design names. A tool not in it draws the gear.
const toolIcons = new Map<string, IconName>([
  ["Read", "doc"],
  ["Write", "edit"],
  ["Edit", "edit"],
  ["MultiEdit", "edit"],
  ["NotebookEdit", "edit"],
  ["Bash", "shell"],
  ["Grep", "search"],
  ["Glob", "search"],
  ["Agent", "agent"],
  ["Task", "agent"],
]);

// toolIcon is the icon a tool's line draws.
function toolIcon(tool: string): IconName {
  return toolIcons.get(tool) ?? "gear";
}

// ToolLine draws a tool or question item as one line of its tool's icon, its tool and its input or question. A call with a
// result is a button: a click shows the result under the line, as an opened element, and a second click folds it again.
// A failed result marks the line with a cross. A running call's line carries the running class and the running mark. The
// result opens when the current match moves into it, and then its match is marked. A reveal the reader gives opens the
// result too, and brings the line into view. A result that counts lines it added or removed draws +N and −M after the
// input. query and current are as ItemRow's are.
export function ToolLine({
  item,
  result,
  running,
  query,
  current,
  reveal,
}: {
  item: ConversationItem;
  result: ConversationItem | undefined;
  running: boolean;
  query: string;
  current: number;
  reveal?: CallReveal;
}) {
  const [shown, setShown] = useState(false);
  const [flashing, flash] = useFlash();
  const element = useRef<HTMLElement>(null);
  const attach = (node: HTMLElement | null): void => {
    element.current = node;
  };
  const called = item.kind === "question" ? item.text : item.input;
  const resultCurrent = result === undefined ? -1 : partCurrent(current, called.length + 1, result.text.length);
  useReveal(resultCurrent >= 0, setShown);
  useLayoutEffect(() => {
    if (reveal !== undefined) {
      setShown(true);
      element.current?.scrollIntoView({ block: "nearest" });
      reveal.done();
    }
  }, [reveal]);
  const className = `line tool call${running ? " running" : ""}${flashing ? " flash" : ""}`;
  const line = (
    <>
      <Icon name={toolIcon(item.tool)} />
      <b>{item.tool}</b>
      <span class="in">
        <MarkedText text={called} query={query} current={partCurrent(current, 0, called.length)} />
      </span>
      {result !== undefined && (result.added > 0n || result.removed > 0n) && (
        <span class="more">
          <span class="plus">{`+${result.added}`}</span> <span class="minus">{`−${result.removed}`}</span>
        </span>
      )}
      {result?.error && <span class="fail">✗</span>}
      {running && <span class="running-mark">running</span>}
    </>
  );
  if (result === undefined) {
    return (
      <div class={className} data-shape="line" ref={attach}>
        {line}
      </div>
    );
  }
  return (
    <>
      <button
        type="button"
        class={className}
        data-shape="line"
        ref={attach}
        onClick={() => {
          flash();
          setShown((value) => !value);
        }}
      >
        {line}
      </button>
      {shown && (
        <div class={result.error ? "opened result err" : "opened result"}>
          <MarkedText text={result.text} query={query} current={resultCurrent} />
        </div>
      )}
    </>
  );
}

// RunCall is one call of a run: its key, its item, its result when it has one, whether it is running, the current match
// in its searched text, and the reveal the reader gives it.
export type RunCall = {
  key: string;
  item: ConversationItem;
  result: ConversationItem | undefined;
  running: boolean;
  current: number;
  reveal?: CallReveal;
};

// ToolRun draws a run of three or more tool calls as one line: its count, the tools with their counts, and a trail of
// one dot per call in call order. Each dot carries its call's status: ok, failed, running, or none for a call with no
// result that is not running. A run holding a running call carries the running class and mark too. A click shows a
// line per call under it, as an opened element, each folding its own result, and a second click folds them again. The run
// opens when the current match moves into one of its calls.
export function ToolRun({ calls, query }: { calls: RunCall[]; query: string }) {
  const [shown, setShown] = useState(false);
  const running = calls.some((call) => call.running);
  useReveal(
    calls.some((call) => call.current >= 0),
    setShown,
  );
  // A reveal of a call in the run opens the run, so the call's line mounts and brings itself into view.
  const reveal = calls.find((call) => call.reveal !== undefined)?.reveal;
  useLayoutEffect(() => {
    if (reveal !== undefined) {
      setShown(true);
    }
  }, [reveal]);
  return (
    <div class="t">
      <span class="gut" />
      <div class="body">
        <div class="line run" data-shape="line">
          <button
            type="button"
            class={running ? "fold calls running" : "fold calls"}
            onClick={() => setShown((value) => !value)}
          >
            <b>{`${calls.length} tool calls`}</b>
            <span class="names">{toolCounts(calls)}</span>
            {running && <span class="running-mark">running</span>}
            <span class="trail">
              {calls.map((call) => (
                <span key={call.key} class={dotClass(call)} />
              ))}
            </span>
          </button>
          {shown && (
            <div class="opened calls">
              {calls.map((call) => (
                <ToolLine
                  key={call.key}
                  item={call.item}
                  result={call.result}
                  running={call.running}
                  query={query}
                  current={call.current}
                  reveal={call.reveal}
                />
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

// sameCalls is whether two runs hold the same calls in the same order: each call has the same key, item, result,
// running state, current match and reveal. A run's calls array is rebuilt on each render, so the calls are what is
// compared, not the array.
export function sameCalls(previous: RunCall[], next: RunCall[]): boolean {
  return (
    previous.length === next.length &&
    previous.every((call, index) => {
      const other = next[index];
      return (
        other !== undefined &&
        call.key === other.key &&
        call.item === other.item &&
        call.result === other.result &&
        call.running === other.running &&
        call.current === other.current &&
        call.reveal === other.reveal
      );
    })
  );
}

// toolCounts names each tool of a run once, in first-seen order, with its count when it is more than one.
function toolCounts(calls: RunCall[]): string {
  const counts = new Map<string, number>();
  for (const call of calls) {
    counts.set(call.item.tool, (counts.get(call.item.tool) ?? 0) + 1);
  }
  return [...counts].map(([tool, count]) => (count > 1 ? `${tool} ×${count}` : tool)).join(", ");
}

// dotClass is the class of a call's dot in a run's trail: running while the call is running, failed when its result
// failed, ok when it succeeded, and none when it has no result and is not running.
function dotClass(call: RunCall): string {
  if (call.running) {
    return "dot running";
  }
  if (call.result === undefined) {
    return "dot none";
  }
  return call.result.error === true ? "dot failed" : "dot ok";
}

// ThinkingLine draws an unfinished thinking block as one line, thinking, that opens to the block's text. A click shows the
// text under the line, and a second click folds it again.
export function ThinkingLine({ text }: { text: string }) {
  const [shown, setShown] = useState(false);
  const [flashing, flash] = useFlash();
  return (
    <>
      <button
        type="button"
        class={flashing ? "line thinking flash" : "line thinking"}
        data-shape="line"
        onClick={() => {
          flash();
          setShown((value) => !value);
        }}
      >
        <Icon name={shown ? "chevronDown" : "chevronRight"} />
        thinking
      </button>
      {shown && <div class="opened">{text}</div>}
    </>
  );
}

// NoticeLine draws a notice as one line: its notice word as the label, then its text. An error notice carries the error
// class. query and current are the item's, as ToolLine's are.
export function NoticeLine({ item, query, current }: { item: ConversationItem; query: string; current: number }) {
  return (
    <div class={item.error ? "line notice err" : "line notice"} data-shape="line">
      <span class="notice-kind">
        <MarkedText text={item.notice} query={query} current={partCurrent(current, 0, item.notice.length)} />
      </span>
      {item.text !== "" && (
        <ReplyText
          text={item.text}
          query={query}
          current={partCurrent(current, item.notice.length + 1, item.text.length)}
        />
      )}
    </div>
  );
}

// EngineFold draws a prompt a step's engine sent as one line that opens to its text: Brief with from the engine for a brief,
// and Engine with the text's first line for an engine prompt. The line is drawn as written and is not marked. The text is
// marked, and it opens when the current match is in it, as useReveal says.
export function EngineFold({ item, query, current }: { item: ConversationItem; query: string; current: number }) {
  const [shown, setShown] = useState(false);
  const [flashing, flash] = useFlash();
  useReveal(current >= 0, setShown);
  const brief = item.sender === "brief";
  const [first] = item.text.split("\n");
  const kind = brief ? "brief" : "engine";
  return (
    <>
      <button
        type="button"
        class={flashing ? `line fold ${kind} flash` : `line fold ${kind}`}
        data-shape="line"
        onClick={() => {
          flash();
          setShown((value) => !value);
        }}
      >
        <Icon name={shown ? "chevronDown" : "chevronRight"} />
        <b>{brief ? "Brief" : "Engine"}</b>
        <span class="names">{brief ? "from the engine" : first}</span>
      </button>
      {shown && (
        <div class="opened">
          <div class="reply">
            <Markdown text={item.text} query={query} current={current} />
          </div>
        </div>
      )}
    </>
  );
}
