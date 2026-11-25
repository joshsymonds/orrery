// The stories of EpicUsageSection: an epic's sessions at the desk, with its steerer shared above the step sessions, and the
// same sessions under 760 px, where a row has no bar. Both draw the epic's fixture, as round 8 section E draws the desk's
// Usage section and section F the phone's rows.

import type { Stories } from "./catalog/story";
import { EpicUsageSection as Section } from "./epicusage";
import { epicRequirements, epicSessions, epicSteps, epicUsage } from "./fixtures/usage";

export const EpicUsageSection: Stories = {
  desk: () => (
    <div class="epic-page">
      <Section
        sessions={epicUsage}
        steps={epicSteps}
        epicSessions={epicSessions}
        requirements={epicRequirements}
        narrow={false}
      />
    </div>
  ),
  phone: () => (
    <div class="epic-page">
      <Section
        sessions={epicUsage}
        steps={epicSteps}
        epicSessions={epicSessions}
        requirements={epicRequirements}
        narrow
      />
    </div>
  ),
};
