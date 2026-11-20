import { keyGroups } from "./keys";

// KeyList draws the keys panel App shows while help is open: the table's five groups, each line's key caps and what the
// key does. Its Close button calls onClose, as ? and esc do through App.
export function KeyList({ onClose }: { onClose: () => void }) {
  return (
    <div class="scrim">
      <section class="panel wide" role="dialog" aria-label="Keys">
        <div class="head">
          <h2 class="t-title">Keys</h2>
          <span class="t-small">
            <kbd>?</kbd> or <kbd>esc</kbd> closes
          </span>
          <button type="button" aria-label="Close" title="Close  esc" onClick={onClose}>
            <svg class="icon" viewBox="0 0 16 16" aria-hidden="true">
              <path d="M4 4l8 8M12 4l-8 8" />
            </svg>
          </button>
        </div>
        <div class="keygroups">
          {keyGroups.map((group) => (
            <div key={group.title} class="keygroup">
              <span class="t-label">{group.title}</span>
              {group.lines.map((line) => (
                <div key={line.text} class="keyline">
                  <span class="ks">
                    {line.caps.map((cap) =>
                      cap === "…" ? (
                        <span key={cap} class="ink-4">
                          …
                        </span>
                      ) : (
                        <kbd key={cap}>{cap}</kbd>
                      ),
                    )}
                  </span>
                  <span>{line.text}</span>
                </div>
              ))}
            </div>
          ))}
        </div>
      </section>
    </div>
  );
}
