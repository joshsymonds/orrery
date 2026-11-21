// The stories of FindBar: the find field with a query that has no match, and with several matches, the current one later
// in them. The bar makes no call, so its handlers do nothing.

import type { Stories } from "./catalog/story";
import { FindBar as Bar } from "./find";

const noop = () => {};

const handlers = { onQuery: noop, onNext: noop, onPrevious: noop, onClose: noop };

export const FindBar: Stories = {
  "no-match": () => <Bar query="zzz" matches={0} current={0} {...handlers} />,
  matches: () => <Bar query="export" matches={4} current={2} {...handlers} />,
};
