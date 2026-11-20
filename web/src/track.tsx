import type { JSX } from "preact";

import type { Stage } from "./recordwords";

// Track draws where an epic is: its five stages in order. A done stage carries a check, the now stage is marked as the
// current step, and each stage says its words. The track opens nothing, so its stages are not links.
export function Track({ stages }: { stages: Stage[] }): JSX.Element {
  return (
    <nav class="track" aria-label="where this epic is">
      {stages.map((stage) => (
        <div
          key={stage.name}
          class={`stage ${stage.state} t-${stage.tone}`}
          aria-current={stage.state === "now" ? "step" : undefined}
        >
          <span class="nm">
            {stage.state === "done" && <span aria-hidden="true">✓</span>}
            {stage.name}
          </span>
          <span class="ds">{stage.words}</span>
        </div>
      ))}
    </nav>
  );
}
