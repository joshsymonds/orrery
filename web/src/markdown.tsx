import type { ComponentChildren } from "preact";
import { useMemo } from "preact/hooks";
import { Lexer, type Token, type Tokens } from "marked";

import { isWebURL, markedText, marksOf, type Marks } from "./text";

const headingTags = ["h1", "h2", "h3", "h4", "h5", "h6"] as const;

// maxDepth is the deepest token level Markdown descends into, counting block and inline levels together. A token past
// it draws its raw source as text, so the element tree and the renderer's stack stay bounded however the text nests.
const maxDepth = 32;

// newline is what drawnText puts between two parts it draws one to a line, and between a line break's two sides.
const newline = "\n";

// Walk is the walk of a reply's drawing through its drawn text, the text drawnText returns. at is the offset in drawn
// text of the next text drawn, and marks is what the query marks there, or undefined when nothing is marked.
interface Walk {
  at: number;
  marks: Marks | undefined;
}

// Markdown draws marked's tokens as the fixed set of elements below. It never builds HTML: markup in the text draws
// as its source text, so the lexer's output is the only input to the element tree. Text the lexer throws on draws
// whole as one text node. With a query, each occurrence of it in the drawn text is a mark, and the one that starts at
// current carries class current. Markdown syntax is never drawn as text, so it is never marked.
export function Markdown({ text, query = "", current = -1 }: { text: string; query?: string; current?: number }) {
  const tokens = useMemo(() => lex(text), [text]);
  const drawn = useMemo(() => (tokens === null ? text : drawnBlocks(tokens, 0)), [text, tokens]);
  const marks = useMemo(() => marksOf(drawn, query, current), [drawn, query, current]);
  const walk: Walk = { at: 0, marks };
  return <>{tokens === null ? markedText(text, 0, marks) : blocks(tokens, 0, walk)}</>;
}

// lex returns marked's tokens for text, or null when the lexer throws on it. Only the lex call is guarded.
function lex(text: string): Token[] | null {
  try {
    return new Lexer({ gfm: true, breaks: true }).lex(text);
  } catch {
    return null;
  }
}

// The render walk below draws what drawnBlock and drawnPhrase return, in the same order and with the same separators,
// so the offset of each text it draws is its offset in drawnText. A block or cell that draws nothing takes no line, as
// lines drops it, and oneToALine moves the walk past the newline between two that draw something.

function blocks(tokens: Token[] = [], depth: number, walk: Walk): ComponentChildren {
  const line = oneToALine(walk);
  return tokens.map((token) => line(() => block(token, depth, walk)));
}

function block(token: Token, depth: number, walk: Walk): ComponentChildren {
  if (depth > maxDepth) {
    return leaf(token.raw, walk);
  }
  switch (token.type) {
    case "space":
      return null;
    case "paragraph":
      return <p>{phrases(token.tokens, depth + 1, walk)}</p>;
    case "text":
      return phrases(token.tokens, depth + 1, walk);
    case "heading": {
      const Tag = headingTags[token.depth - 1];
      return <Tag>{phrases(token.tokens, depth + 1, walk)}</Tag>;
    }
    case "code":
      return (
        <pre>
          <code>{leaf(token.text, walk)}</code>
        </pre>
      );
    case "blockquote":
      return <blockquote>{blocks(token.tokens, depth + 1, walk)}</blockquote>;
    case "hr":
      return <hr />;
    case "list": {
      const line = oneToALine(walk);
      const items = token.items.map((item: Tokens.ListItem) =>
        line(() => <li>{blocks(item.tokens, depth + 1, walk)}</li>),
      );
      return token.ordered ? <ol start={token.start}>{items}</ol> : <ul>{items}</ul>;
    }
    case "table": {
      const line = oneToALine(walk);
      return (
        <table>
          <thead>
            <tr>
              {token.header.map((cell: Tokens.TableCell) =>
                line(() => <th>{phrases(cell.tokens, depth + 1, walk)}</th>),
              )}
            </tr>
          </thead>
          <tbody>
            {token.rows.map((row: Tokens.TableCell[]) => (
              <tr>
                {row.map((cell) => line(() => <td>{phrases(cell.tokens, depth + 1, walk)}</td>))}
              </tr>
            ))}
          </tbody>
        </table>
      );
    }
    default:
      return leaf(token.raw, walk);
  }
}

function phrases(tokens: Token[] = [], depth: number, walk: Walk): ComponentChildren {
  return tokens.map((token) => phrase(token, depth, walk));
}

// Text draws its source as written. marked's text also decodes numeric character references, which would hide the
// source the reader typed.
function phrase(token: Token, depth: number, walk: Walk): ComponentChildren {
  if (depth > maxDepth) {
    return leaf(token.raw, walk);
  }
  switch (token.type) {
    case "text":
      return leaf(token.raw, walk);
    case "escape":
      return leaf(token.text, walk);
    case "codespan":
      return <code>{leaf(token.text, walk)}</code>;
    case "em":
      return <em>{phrases(token.tokens, depth + 1, walk)}</em>;
    case "strong":
      return <strong>{phrases(token.tokens, depth + 1, walk)}</strong>;
    case "del":
      return <del>{phrases(token.tokens, depth + 1, walk)}</del>;
    case "br":
      walk.at += newline.length;
      return <br />;
    case "link":
      return isWebURL(token.href) ? (
        <a href={token.href} rel="noopener noreferrer" target="_blank">
          {phrases(token.tokens, depth + 1, walk)}
        </a>
      ) : (
        phrases(token.tokens, depth + 1, walk)
      );
    case "image":
      return leaf(token.text, walk);
    default:
      return leaf(token.raw, walk);
  }
}

// leaf draws a run of drawn text where the walk is, and moves the walk past it.
function leaf(text: string, walk: Walk): ComponentChildren {
  const at = walk.at;
  walk.at += text.length;
  return markedText(text, at, walk.marks);
}

// oneToALine returns how a sequence draws its parts one to a line: each part is passed to the returned function, which
// draws it. A part that draws nothing takes no line, and a part after one that drew something starts after a newline.
function oneToALine(walk: Walk): (draw: () => ComponentChildren) => ComponentChildren {
  let drawn = false;
  return (draw) => {
    const start = walk.at;
    const separator = drawn ? newline.length : 0;
    walk.at = start + separator;
    const node = draw();
    if (walk.at === start + separator) {
      walk.at = start;
    } else {
      drawn = true;
    }
    return node;
  };
}

// drawnText returns the text Markdown draws for text: the walk above, keeping each element's text and dropping its
// markup, so a link draws its label and not its target. Each block is a line of its own, so two blocks never run
// together into one word. Text the lexer throws on draws whole, as Markdown draws it.
export function drawnText(text: string): string {
  const tokens = lex(text);
  return tokens === null ? text : drawnBlocks(tokens, 0);
}

function drawnBlocks(tokens: Token[] = [], depth: number): string {
  return lines(tokens.map((token) => drawnBlock(token, depth)));
}

function drawnBlock(token: Token, depth: number): string {
  if (depth > maxDepth) {
    return token.raw;
  }
  switch (token.type) {
    case "space":
    case "hr":
      return "";
    case "paragraph":
    case "text":
    case "heading":
      return drawnPhrases(token.tokens, depth + 1);
    case "code":
      return token.text;
    case "blockquote":
      return drawnBlocks(token.tokens, depth + 1);
    case "list":
      return lines(token.items.map((item: Tokens.ListItem) => drawnBlocks(item.tokens, depth + 1)));
    case "table":
      return lines(
        [...token.header, ...token.rows.flat()].map((cell: Tokens.TableCell) => drawnPhrases(cell.tokens, depth + 1)),
      );
    default:
      return token.raw;
  }
}

function drawnPhrases(tokens: Token[] = [], depth: number): string {
  return tokens.map((token) => drawnPhrase(token, depth)).join("");
}

function drawnPhrase(token: Token, depth: number): string {
  if (depth > maxDepth) {
    return token.raw;
  }
  switch (token.type) {
    case "text":
      return token.raw;
    case "escape":
    case "codespan":
    case "image":
      return token.text;
    case "em":
    case "strong":
    case "del":
    case "link":
      return drawnPhrases(token.tokens, depth + 1);
    case "br":
      return "\n";
    default:
      return token.raw;
  }
}

// lines joins the non-empty parts, one to a line.
function lines(parts: string[]): string {
  return parts.filter((part) => part !== "").join("\n");
}
