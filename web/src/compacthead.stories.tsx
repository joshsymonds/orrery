// The stories of CompactHead, the phone header folded to one line: the badges with nothing asking, and the same badges
// with the conversation's rota epic asking.

import type { Stories } from "./catalog/story";
import * as compacthead from "./compacthead";
import type { CompactBadge } from "./compacthead";

const ignore = () => {};

const rota: CompactBadge = { key: "rota", label: "rota", count: 0, asks: true };
const rotaQuiet: CompactBadge = { key: "rota", label: "rota", count: 0, asks: false };
const agents: CompactBadge = { key: "agents", label: "agents", count: 2, asks: false };
const running: CompactBadge = { key: "running", label: "running", count: 1, asks: false };

export const CompactHead: Stories = {
  quiet: () => <compacthead.CompactHead asking="" badges={[rotaQuiet, agents, running]} onOpen={ignore} />,
  asking: () => <compacthead.CompactHead asking="rota-export asks" badges={[rota, agents, running]} onOpen={ignore} />,
};
