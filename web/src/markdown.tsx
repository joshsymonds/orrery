import type { ComponentChildren } from "preact";
import { useMemo } from "preact/hooks";
import { Lexer, type Token, type Tokens } from "marked";

import { isWebURL } from "./text";

const headingTags = ["h1", "h2", "h3", "h4", "h5", "h6"] as const;

// maxDepth is the deepest token level Markdown descends into, counting block and inline levels together. A token past
// it draws its raw source as text, so the element tree and the renderer's stack stay bounded however the text nests.
const maxDepth = 32;

// Markdown draws marked's tokens as the fixed set of elements below. It never builds HTML: markup in the text draws
// as its source text, so the lexer's output is the only input to the element tree. Text the lexer throws on draws
// whole as one text node.
export function Markdown({ text }: { text: string }) {
  const tokens = useMemo(() => lex(text), [text]);
  return <>{tokens === null ? text : blocks(tokens, 0)}</>;
}

// lex returns marked's tokens for text, or null when the lexer throws on it. Only the lex call is guarded.
function lex(text: string): Token[] | null {
  try {
    return new Lexer({ gfm: true, breaks: true }).lex(text);
  } catch {
    return null;
  }
}

function blocks(tokens: Token[] = [], depth: number): ComponentChildren {
  return tokens.map((token) => block(token, depth));
}

function block(token: Token, depth: number): ComponentChildren {
  if (depth > maxDepth) {
    return token.raw;
  }
  switch (token.type) {
    case "space":
      return null;
    case "paragraph":
      return <p>{phrases(token.tokens, depth + 1)}</p>;
    case "text":
      return phrases(token.tokens, depth + 1);
    case "heading": {
      const Tag = headingTags[token.depth - 1];
      return <Tag>{phrases(token.tokens, depth + 1)}</Tag>;
    }
    case "code":
      return (
        <pre>
          <code>{token.text}</code>
        </pre>
      );
    case "blockquote":
      return <blockquote>{blocks(token.tokens, depth + 1)}</blockquote>;
    case "hr":
      return <hr />;
    case "list": {
      const items = token.items.map((item: Tokens.ListItem) => <li>{blocks(item.tokens, depth + 1)}</li>);
      return token.ordered ? <ol start={token.start}>{items}</ol> : <ul>{items}</ul>;
    }
    case "table":
      return (
        <table>
          <thead>
            <tr>
              {token.header.map((cell: Tokens.TableCell) => (
                <th>{phrases(cell.tokens, depth + 1)}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {token.rows.map((row: Tokens.TableCell[]) => (
              <tr>
                {row.map((cell) => (
                  <td>{phrases(cell.tokens, depth + 1)}</td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      );
    default:
      return token.raw;
  }
}

function phrases(tokens: Token[] = [], depth: number): ComponentChildren {
  return tokens.map((token) => phrase(token, depth));
}

// Text draws its source as written. marked's text also decodes numeric character references, which would hide the
// source the reader typed.
function phrase(token: Token, depth: number): ComponentChildren {
  if (depth > maxDepth) {
    return token.raw;
  }
  switch (token.type) {
    case "text":
      return token.raw;
    case "escape":
      return token.text;
    case "codespan":
      return <code>{token.text}</code>;
    case "em":
      return <em>{phrases(token.tokens, depth + 1)}</em>;
    case "strong":
      return <strong>{phrases(token.tokens, depth + 1)}</strong>;
    case "del":
      return <del>{phrases(token.tokens, depth + 1)}</del>;
    case "br":
      return <br />;
    case "link":
      return isWebURL(token.href) ? (
        <a href={token.href} rel="noopener noreferrer" target="_blank">
          {phrases(token.tokens, depth + 1)}
        </a>
      ) : (
        phrases(token.tokens, depth + 1)
      );
    case "image":
      return token.text;
    default:
      return token.raw;
  }
}
