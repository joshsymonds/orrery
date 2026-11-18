// The stories of FindBar: the find field over a transcript, empty, with no match, and with a match that is first or
// later. The bar makes no call, so its handlers do nothing.

import type { Stories } from "./catalog/story";
import { FindBar as Bar } from "./find";

const noop = () => {};

const handlers = { onQuery: noop, onNext: noop, onPrevious: noop, onClose: noop };

export const FindBar: Stories = {
  empty: () => <Bar query="" matches={0} current={0} {...handlers} />,
  "no-matches": () => <Bar query="zzz" matches={0} current={0} {...handlers} />,
  "one-match": () => <Bar query="migration" matches={1} current={0} {...handlers} />,
  "later-match": () => <Bar query="export" matches={4} current={2} {...handlers} />,
};
