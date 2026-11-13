import assert from "node:assert/strict";
import { mock, test } from "node:test";

import { Lexer } from "marked";
import { render } from "preact";

import { Markdown } from "./markdown";
import { installTestDom, removeTestDom } from "./testdom";

const fixedElements = new Set([
  "a",
  "blockquote",
  "br",
  "code",
  "del",
  "em",
  "h1",
  "h2",
  "h3",
  "h4",
  "h5",
  "h6",
  "hr",
  "li",
  "ol",
  "p",
  "pre",
  "strong",
  "table",
  "tbody",
  "td",
  "th",
  "thead",
  "tr",
  "ul",
]);

async function withMarkdown(text: string, check: (container: HTMLElement) => void): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(<Markdown text={text} />, container);
    check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

// deepestElement returns how many elements nest on the deepest path under container, container excluded.
function deepestElement(container: HTMLElement): number {
  let deepest = 0;
  for (const element of container.querySelectorAll("*")) {
    let depth = 0;
    for (let node: Element | null = element; node !== null && node !== container; node = node.parentElement) {
      depth++;
    }
    deepest = Math.max(deepest, depth);
  }
  return deepest;
}

// elementCap is the deepest element nesting Markdown draws: the top-level element and the 32 token levels under it.
const elementCap = 33;

test("prose draws as a paragraph with < and & literal", () =>
  withMarkdown("a < b & c", (container) => {
    const paragraphs = container.querySelectorAll("p");
    assert.equal(paragraphs.length, 1);
    assert.equal(paragraphs[0].textContent, "a < b & c");
  }));

test("entity references draw as typed, not decoded", () =>
  withMarkdown("a &amp; b &#60; c", (container) => {
    assert.equal(container.textContent, "a &amp; b &#60; c");
  }));

test("one newline draws a line break", () =>
  withMarkdown("one\ntwo", (container) => {
    const paragraph = container.querySelector("p")!;
    assert.equal(paragraph.querySelectorAll("br").length, 1);
    assert.equal(paragraph.textContent, "onetwo");
  }));

test("bullet list draws ul with li items and < and & literal", () =>
  withMarkdown("- one < x\n- two & y", (container) => {
    const items = container.querySelectorAll("ul > li");
    assert.equal(items.length, 2);
    assert.equal(items[0].textContent, "one < x");
    assert.equal(items[1].textContent, "two & y");
  }));

test("ordered list draws ol with its start number", () =>
  withMarkdown("3. three\n4. four", (container) => {
    assert.equal(container.querySelector("ol")?.getAttribute("start"), "3");
    const items = [...container.querySelectorAll("ol > li")].map((item) => item.textContent);
    assert.deepEqual(items, ["three", "four"]);
  }));

test("code block draws pre and code with < and & literal", () =>
  withMarkdown("```\nlet x = a < b && c;\n```", (container) => {
    const code = container.querySelectorAll("pre > code");
    assert.equal(code.length, 1);
    assert.equal(code[0].textContent, "let x = a < b && c;");
  }));

test("inline code draws code with < and & literal", () =>
  withMarkdown("use `a < b && c` here", (container) => {
    const code = container.querySelectorAll("p > code");
    assert.equal(code.length, 1);
    assert.equal(code[0].textContent, "a < b && c");
    assert.equal(container.textContent, "use a < b && c here");
  }));

test("emphasis draws em", () =>
  withMarkdown("*soft* text", (container) => {
    assert.equal(container.querySelector("em")?.textContent, "soft");
    assert.equal(container.textContent, "soft text");
  }));

test("strong draws strong", () =>
  withMarkdown("**bold** text", (container) => {
    assert.equal(container.querySelector("strong")?.textContent, "bold");
    assert.equal(container.textContent, "bold text");
  }));

test("strikethrough draws del", () =>
  withMarkdown("~~gone~~ text", (container) => {
    assert.equal(container.querySelector("del")?.textContent, "gone");
    assert.equal(container.textContent, "gone text");
  }));

test("headings draw h1 and h3 by depth", () =>
  withMarkdown("# one\n\n### three", (container) => {
    assert.equal(container.querySelector("h1")?.textContent, "one");
    assert.equal(container.querySelector("h3")?.textContent, "three");
    assert.equal(container.querySelectorAll("h1, h2, h3, h4, h5, h6").length, 2);
  }));

test("table draws thead and tbody cells with < and & literal", () =>
  withMarkdown("| a | b < c |\n| - | - |\n| 1 < 2 | 3 & 4 |", (container) => {
    const headers = [...container.querySelectorAll("thead th")].map((cell) => cell.textContent);
    assert.deepEqual(headers, ["a", "b < c"]);
    const cells = [...container.querySelectorAll("tbody td")].map((cell) => cell.textContent);
    assert.deepEqual(cells, ["1 < 2", "3 & 4"]);
  }));

test("quote draws blockquote around its paragraph", () =>
  withMarkdown("> quoted < text", (container) => {
    assert.equal(container.querySelector("blockquote > p")?.textContent, "quoted < text");
  }));

test("rule draws hr", () =>
  withMarkdown("above\n\n---\n\nbelow", (container) => {
    assert.equal(container.querySelectorAll("hr").length, 1);
    assert.equal(container.textContent, "abovebelow");
  }));

test("web link draws a with the reply link attributes", () =>
  withMarkdown("[docs](https://example.test/docs)", (container) => {
    const links = container.querySelectorAll("a");
    assert.equal(links.length, 1);
    assert.equal(links[0].getAttribute("href"), "https://example.test/docs");
    assert.equal(links[0].textContent, "docs");
    assert.equal(links[0].getAttribute("rel"), "noopener noreferrer");
    assert.equal(links[0].getAttribute("target"), "_blank");
  }));

test("link failing isWebURL draws its text only", () =>
  withMarkdown("[click](javascript:alert(1)) and [data](data:text/html,x)", (container) => {
    assert.equal(container.querySelectorAll("a").length, 0);
    assert.equal(container.textContent, "click and data");
  }));

test("block HTML draws its source as text", () =>
  withMarkdown('<div class="x">raw < text</div>', (container) => {
    assert.ok(container.querySelector("div") === null);
    assert.equal(container.textContent, '<div class="x">raw < text</div>');
  }));

test("inline HTML draws its source as text", () =>
  withMarkdown("a <b>bold</b> c", (container) => {
    assert.ok(container.querySelector("b") === null);
    assert.equal(container.textContent, "a <b>bold</b> c");
  }));

test("image draws its alt text", () =>
  withMarkdown("![a cat](https://example.test/cat.png)", (container) => {
    assert.ok(container.querySelector("img") === null);
    assert.equal(container.textContent, "a cat");
  }));

test("an unknown token draws its source as text", () =>
  withMarkdown("[ref]: https://example.test/r", (container) => {
    assert.equal(container.querySelectorAll("a").length, 0);
    assert.equal(container.textContent, "[ref]: https://example.test/r");
  }));

const hostile = [
  "<script>alert(1)</script>",
  "",
  "<img src=x onerror=alert(1)>",
  "",
  '<div style="color:red" onclick="alert(1)">raw block</div>',
  "",
  "[bad](javascript:alert(1)) [worse](data:text/html,x) [ok](https://example.test/ok)",
  "",
  "![pic](https://example.test/p.png)",
  "",
  "a <span onmouseover=alert(1)>inline</span> tag",
].join("\n");

test("hostile markup yields only fixed-list elements, http(s) links, and its source as text", () =>
  withMarkdown(hostile, (container) => {
    for (const element of container.querySelectorAll("*")) {
      const tag = element.tagName.toLowerCase();
      assert.ok(fixedElements.has(tag), `unexpected <${tag}>`);
      for (const name of element.getAttributeNames()) {
        assert.ok(!name.startsWith("on") && name !== "style", `<${tag}> has ${name}`);
      }
    }
    const links = container.querySelectorAll("a");
    assert.equal(links.length, 1);
    assert.equal(links[0].getAttribute("href"), "https://example.test/ok");
    const text = container.textContent ?? "";
    assert.ok(text.includes("<script>alert(1)</script>"));
    assert.ok(text.includes("<img src=x onerror=alert(1)>"));
    assert.ok(text.includes('<div style="color:red" onclick="alert(1)">raw block</div>'));
    assert.ok(text.includes("<span onmouseover=alert(1)>inline</span>"));
  }));

test("block nesting past depth 32 draws its source as text with no element deeper than the cap", () => {
  const text = ">".repeat(40) + " x";
  return withMarkdown(text, (container) => {
    assert.equal(deepestElement(container) <= elementCap, true, "no element nests past the cap");
    assert.equal(container.querySelectorAll("blockquote").length, 33);
    assert.equal(container.textContent, ">".repeat(7) + " x");
  });
});

test("inline nesting past depth 32 draws its source as text with no element deeper than the cap", () => {
  const text = "*".repeat(200) + "word" + "*".repeat(200);
  return withMarkdown(text, (container) => {
    assert.equal(deepestElement(container) <= elementCap, true, "no element nests past the cap");
    assert.equal(container.querySelectorAll("strong").length, 32);
    assert.equal(container.textContent, "*".repeat(136) + "word" + "*".repeat(136));
  });
});

test("text marked cannot lex draws as one text node", () =>
  withMarkdown(">".repeat(2000) + " x", (container) => {
    assert.equal(container.childNodes.length, 1);
    assert.equal(container.firstChild?.nodeType, Node.TEXT_NODE);
    assert.equal(container.textContent, ">".repeat(2000) + " x");
  }));

test("re-rendering unchanged text does not lex it again", async () => {
  const window = installTestDom();
  const container = document.createElement("div");
  const lex = mock.method(Lexer.prototype, "lex");
  try {
    render(<Markdown text="same *text*" />, container);
    render(<Markdown text="same *text*" />, container);
    assert.equal(lex.mock.callCount(), 1);
    render(<Markdown text="changed *text*" />, container);
    assert.equal(lex.mock.callCount(), 2);
  } finally {
    mock.restoreAll();
    render(null, container);
    await removeTestDom(window);
  }
});
