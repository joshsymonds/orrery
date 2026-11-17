import type { ComponentChildren } from "preact";
import { useLayoutEffect, useRef } from "preact/hooks";

// A candidate is a run of non-space characters that starts with a scheme and "://". Only candidates whose
// parsed protocol is http: or https: become links; every other candidate stays text.
const candidate = /[A-Za-z][A-Za-z0-9+.-]*:\/\/\S*/g;
const trailingPunctuation = /[.,;:!?)\]}'"]+$/;

// Range is one occurrence of a query: the offset of its first character, and the offset just past its last.
type Range = [start: number, end: number];

// Marks is what marks a text's occurrences of a query. Its offsets are the offsets of the text it marks, and current is
// the offset the current occurrence starts at, or -1 when the current match is not in the text.
export interface Marks {
  ranges: Range[];
  current: number;
}

// occurrences returns the case-insensitive occurrences of query in text, in order and never overlapping. An empty query
// has none.
export function occurrences(text: string, query: string): Range[] {
  if (query === "") {
    return [];
  }
  const pattern = new RegExp(escapeRegExp(query), "gi");
  return [...text.matchAll(pattern)].map((found): Range => [found.index, found.index + found[0].length]);
}

function escapeRegExp(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

// marksOf returns what marks the occurrences of query in text, the current one being the occurrence that starts at
// current. It is undefined when the text has no occurrence, so nothing in it is marked.
export function marksOf(text: string, query: string, current: number): Marks | undefined {
  const ranges = occurrences(text, query);
  return ranges.length === 0 ? undefined : { ranges, current };
}

// markedText draws text, which starts at offset at of the text marks are over. Each part of it inside an occurrence is
// a mark, so an occurrence that a Markdown element splits is marked on each part. The part where the current occurrence
// starts carries class current, and no other mark does. Without marks the text draws as it is.
export function markedText(text: string, at: number, marks: Marks | undefined): ComponentChildren {
  if (marks === undefined) {
    return text;
  }
  const end = at + text.length;
  const pieces: ComponentChildren[] = [];
  let drawn = at;
  for (const [start, stop] of marks.ranges) {
    if (stop <= drawn) {
      continue;
    }
    if (start >= end) {
      break;
    }
    const from = Math.max(start, drawn);
    const to = Math.min(stop, end);
    if (from > drawn) {
      pieces.push(text.slice(drawn - at, from - at));
    }
    const part = text.slice(from - at, to - at);
    if (start === marks.current && from === start) {
      // Keyed by its offset, so the mark mounts again, and scrolls, when the current match moves to another occurrence.
      pieces.push(<CurrentMark key={start} text={part} />);
    } else {
      pieces.push(<mark>{part}</mark>);
    }
    drawn = to;
  }
  if (drawn < end) {
    pieces.push(text.slice(drawn - at));
  }
  return pieces;
}

// CurrentMark is the mark of the current occurrence. It brings itself into view when it mounts, which is when the
// current match arrives at this occurrence or a fold holding it opens.
function CurrentMark({ text }: { text: string }) {
  const mark = useRef<HTMLElement>(null);
  useLayoutEffect(() => {
    mark.current?.scrollIntoView({ block: "nearest" });
  }, []);
  return (
    <mark ref={mark} class="current">
      {text}
    </mark>
  );
}

// MarkedText draws text as text, marking each occurrence of query. The one that starts at current is the current one.
export function MarkedText({ text, query = "", current = -1 }: { text: string; query?: string; current?: number }) {
  return <>{markedText(text, 0, marksOf(text, query, current))}</>;
}

export function ReplyText({ text, query = "", current = -1 }: { text: string; query?: string; current?: number }) {
  const marks = marksOf(text, query, current);
  const children: ComponentChildren[] = [];
  let kept = 0;
  for (const match of text.matchAll(candidate)) {
    const start = match.index;
    const url = match[0].replace(trailingPunctuation, "");
    if (!isWebURL(url)) {
      continue;
    }
    if (start > kept) {
      children.push(markedText(text.slice(kept, start), kept, marks));
    }
    children.push(
      <a href={url} rel="noopener noreferrer" target="_blank">
        {markedText(url, start, marks)}
      </a>,
    );
    kept = start + url.length;
  }
  if (kept < text.length) {
    children.push(markedText(text.slice(kept), kept, marks));
  }
  return <span class="reply">{children}</span>;
}

export function isWebURL(candidate: string): boolean {
  try {
    const { protocol } = new URL(candidate);
    return protocol === "http:" || protocol === "https:";
  } catch {
    return false;
  }
}
