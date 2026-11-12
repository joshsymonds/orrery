import type { ComponentChildren } from "preact";
import { useMemo } from "preact/hooks";
import { Lexer, type Token, type Tokens } from "marked";

import { isWebURL } from "./text";

const headingTags = ["h1", "h2", "h3", "h4", "h5", "h6"] as const;

// Markdown draws marked's tokens as the fixed set of elements below. It never builds HTML: markup in the text draws
// as its source text, so the lexer's output is the only input to the element tree.
export function Markdown({ text }: { text: string }) {
  const tokens = useMemo(() => new Lexer({ gfm: true, breaks: true }).lex(text), [text]);
  return <>{blocks(tokens)}</>;
}

function blocks(tokens: Token[] = []): ComponentChildren {
  return tokens.map(block);
}

function block(token: Token): ComponentChildren {
  switch (token.type) {
    case "space":
      return null;
    case "paragraph":
      return <p>{phrases(token.tokens)}</p>;
    case "text":
      return phrases(token.tokens);
    case "heading": {
      const Tag = headingTags[token.depth - 1];
      return <Tag>{phrases(token.tokens)}</Tag>;
    }
    case "code":
      return (
        <pre>
          <code>{token.text}</code>
        </pre>
      );
    case "blockquote":
      return <blockquote>{blocks(token.tokens)}</blockquote>;
    case "hr":
      return <hr />;
    case "list": {
      const items = token.items.map((item: Tokens.ListItem) => <li>{blocks(item.tokens)}</li>);
      return token.ordered ? <ol start={token.start}>{items}</ol> : <ul>{items}</ul>;
    }
    case "table":
      return (
        <table>
          <thead>
            <tr>
              {token.header.map((cell: Tokens.TableCell) => (
                <th>{phrases(cell.tokens)}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {token.rows.map((row: Tokens.TableCell[]) => (
              <tr>
                {row.map((cell) => (
                  <td>{phrases(cell.tokens)}</td>
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

function phrases(tokens: Token[] = []): ComponentChildren {
  return tokens.map(phrase);
}

// Text draws its source as written. marked's text also decodes numeric character references, which would hide the
// source the reader typed.
function phrase(token: Token): ComponentChildren {
  switch (token.type) {
    case "text":
      return token.raw;
    case "escape":
      return token.text;
    case "codespan":
      return <code>{token.text}</code>;
    case "em":
      return <em>{phrases(token.tokens)}</em>;
    case "strong":
      return <strong>{phrases(token.tokens)}</strong>;
    case "del":
      return <del>{phrases(token.tokens)}</del>;
    case "br":
      return <br />;
    case "link":
      return isWebURL(token.href) ? (
        <a href={token.href} rel="noopener noreferrer" target="_blank">
          {phrases(token.tokens)}
        </a>
      ) : (
        phrases(token.tokens)
      );
    case "image":
      return token.text;
    default:
      return token.raw;
  }
}
