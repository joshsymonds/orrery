import assert from "node:assert/strict";
import { test } from "node:test";

import { render } from "preact";

import { ReplyText } from "./text";
import { installTestDom, removeTestDom } from "./testdom";

async function withReply(text: string, check: (container: HTMLElement) => void): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(<ReplyText text={text} />, container);
    check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

test("script markup renders as text, not an element", () =>
  withReply("<script>alert(1)</script>", (container) => {
    assert.equal(container.querySelector("script"), null);
    assert.equal(container.textContent, "<script>alert(1)</script>");
  }));

test("img markup renders as text, not an element", () =>
  withReply("<img src=x onerror=alert(1)>", (container) => {
    assert.equal(container.querySelector("img"), null);
    assert.equal(container.textContent, "<img src=x onerror=alert(1)>");
  }));

test("non-http schemes render no link", async () => {
  for (const text of ["javascript:alert(1)", "data:text/html,x", "ftp://example.test"]) {
    await withReply(text, (container) => {
      assert.equal(container.querySelectorAll("a").length, 0, `${text} produced a link`);
      assert.equal(container.textContent, text);
    });
  }
});

test("a bare https scheme with no host renders no link", () =>
  withReply("https://", (container) => {
    assert.equal(container.querySelectorAll("a").length, 0);
    assert.equal(container.textContent, "https://");
  }));

test("https and http URLs render links with the exact href", async () => {
  for (const url of ["https://example.test/a?b=1", "http://example.test"]) {
    await withReply(url, (container) => {
      const links = container.querySelectorAll("a");
      assert.equal(links.length, 1, `${url} produced ${links.length} links`);
      assert.equal(links[0].getAttribute("href"), url);
      assert.equal(links[0].textContent, url);
      assert.equal(links[0].getAttribute("rel"), "noopener noreferrer");
      assert.equal(links[0].getAttribute("target"), "_blank");
    });
  }
});

test("scheme matching is case-insensitive", () =>
  withReply("HTTPS://EXAMPLE.TEST/X", (container) => {
    const links = container.querySelectorAll("a");
    assert.equal(links.length, 1);
    assert.equal(links[0].getAttribute("href"), "HTTPS://EXAMPLE.TEST/X");
  }));

test("trailing sentence punctuation stays text", () =>
  withReply("see https://example.test/x.", (container) => {
    const links = container.querySelectorAll("a");
    assert.equal(links.length, 1);
    assert.equal(links[0].getAttribute("href"), "https://example.test/x");
    assert.equal(links[0].textContent, "https://example.test/x");
    assert.equal(container.textContent, "see https://example.test/x.");
    assert.equal(container.querySelector("span")?.lastChild?.textContent, ".");
  }));

test("closing brackets around a URL stay text", () =>
  withReply("(https://example.test/x)", (container) => {
    const links = container.querySelectorAll("a");
    assert.equal(links.length, 1);
    assert.equal(links[0].getAttribute("href"), "https://example.test/x");
    assert.equal(container.textContent, "(https://example.test/x)");
  }));

test("text before and after a link is kept in order", () =>
  withReply("before https://example.test/a after", (container) => {
    const nodes = [...container.querySelector("span")!.childNodes];
    assert.equal(nodes.length, 3);
    assert.equal(nodes[0].nodeType, nodes[0].TEXT_NODE);
    assert.equal(nodes[0].textContent, "before ");
    assert.equal((nodes[1] as Element).tagName, "A");
    assert.equal((nodes[1] as Element).getAttribute("href"), "https://example.test/a");
    assert.equal(nodes[2].textContent, " after");
  }));
