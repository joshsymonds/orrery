// The stories of MarkedText and ReplyText: text with its query's occurrences marked, the current one among them, and reply
// text whose web links are links and whose other schemes stay text.

import type { Stories } from "./catalog/story";
import * as text from "./text";

const repeated = "Export refuses to overwrite; export writes a CSV";

export const MarkedText: Stories = {
  "one-occurrence": () => <text.MarkedText text="the rota export writes one file" query="export" />,
  "every-occurrence": () => <text.MarkedText text={repeated} query="EXPORT" />,
  "current-occurrence": () => (
    <text.MarkedText text={repeated} query="export" current={repeated.indexOf("export", 1)} />
  ),
};

export const ReplyText: Stories = {
  plain: () => <text.ReplyText text="adding retry backoff" />,
  link: () => <text.ReplyText text="PR at https://github.com/orrery/orrery/pull/12 is ready" />,
  "non-web-scheme": () => <text.ReplyText text="copy ftp://files.example.com/rota.csv by hand" />,
  "marks-in-a-link": () => <text.ReplyText text="see https://example.com/rota for the rota" query="rota" />,
};
