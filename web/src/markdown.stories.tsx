// The stories of Markdown: emphasis and code in a paragraph, a list, a fenced code block, a table, a heading with a link,
// and text whose query occurrence is marked, the current one among them.

import type { Stories } from "./catalog/story";
import * as markdown from "./markdown";

const marked = "export writes CSV, export refuses to overwrite";

export const Markdown: Stories = {
  paragraph: () => <markdown.Markdown text="The **export** writes *one* row per shift, see `cli/export.go`." />,
  list: () => <markdown.Markdown text={"- refuse to overwrite\n- write an audit line\n- keep the header row"} />,
  code: () => <markdown.Markdown text={"```go\nfunc Export() error {\n\treturn nil\n}\n```"} />,
  table: () => <markdown.Markdown text={"| keeper | shifts |\n| --- | --- |\n| Ana | 12 |"} />,
  "heading-and-link": () => (
    <markdown.Markdown text={"## Rota export\n\nRead [the plan](https://example.com/plan) first."} />
  ),
  "current-occurrence": () => (
    <markdown.Markdown text={marked} query="export" current={marked.indexOf("export", 1)} />
  ),
};
