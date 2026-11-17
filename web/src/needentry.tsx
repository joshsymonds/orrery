import type { JSX } from "preact";

import { AskFirst } from "./blocker";
import type { EpicCommands } from "./epicpage";
import type { Need } from "./needs";

// NeedEntry draws one epic need: its sentence, which opens the epic on click, then its control. A question draws a
// button that opens its epic, where its options are answered; a park draws Retry, and a release need draws Retry and
// Stop…, which asks first. Any other need draws its sentence alone.
export function NeedEntry({
  need,
  slug,
  commands,
  onOpen,
}: {
  need: Need;
  slug: string;
  commands: EpicCommands;
  onOpen: () => void;
}): JSX.Element {
  return (
    <div class="need-entry">
      <p class="need-text" onClick={() => onOpen()}>
        {need.text}
      </p>
      {need.kind === "question" && (
        <button type="button" class="act" onClick={() => onOpen()}>
          Open the epic ›
        </button>
      )}
      {need.kind === "park" && (
        <button type="button" class="act" onClick={() => commands.retry()}>
          Retry
        </button>
      )}
      {need.kind === "release" && (
        <>
          <button type="button" class="act" onClick={() => commands.retry()}>
            Retry
          </button>
          <AskFirst
            label="Stop…"
            question={`Stop ${slug}?`}
            confirm={`Stop ${slug}`}
            keep="Keep it running"
            onConfirm={() => commands.stop()}
          />
        </>
      )}
    </div>
  );
}
