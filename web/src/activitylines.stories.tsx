// The stories of ActivityLines: one running sub-agent, several things running, four running folded to three lines and a
// count, a line lit while its list panel is open, and nothing running, which draws no container.

import { ActivityLines as Lines } from "./activitylines";
import type { Stories } from "./catalog/story";
import type { ActivityLine } from "./work";

const noop = () => undefined;

// now is the clock the fixture's times are read from, in unix seconds.
const now = 1_700_000_000;

const explore: ActivityLine = {
  key: "task-explore",
  type: "agent",
  who: "Explore",
  what: "Find every caller of keeperFor",
  doing: "Grep keeperFor\\(",
  started: now - 131,
  list: "agents",
};

const reviewer: ActivityLine = {
  key: "task-reviewer",
  type: "agent",
  who: "reviewer",
  what: "Review the query change",
  doing: "Read query.go",
  started: now - 48,
  list: "agents",
};

const dev: ActivityLine = {
  key: "task-dev",
  type: "shell",
  who: "shell",
  what: "pnpm dev --port 5173",
  doing: "ready on :5173",
  started: now - 2460,
  list: "running",
};

const warm: ActivityLine = {
  key: "task-warm",
  type: "shell",
  who: "shell",
  what: "sleep 1800 && just warm",
  doing: "",
  started: now - 720,
  list: "running",
};

export const ActivityLines: Stories = {
  one: () => <Lines lines={[explore]} now={now} open={null} onOpen={noop} />,
  several: () => <Lines lines={[explore, reviewer, dev]} now={now} open={null} onOpen={noop} />,
  many: () => <Lines lines={[explore, reviewer, dev, warm]} now={now} open={null} onOpen={noop} />,
  open: () => <Lines lines={[explore, reviewer, dev]} now={now} open="agents" onOpen={noop} />,
  nothing: () => (
    <>
      <Lines lines={[]} now={now} open={null} onOpen={noop} />
      <p class="inote">
        <span>nothing is drawn: nothing is running</span>
      </p>
    </>
  ),
};
