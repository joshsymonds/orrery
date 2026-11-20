import type { ComponentChildren, JSX } from "preact";
import { useEffect, useRef } from "preact/hooks";

export interface SheetProps {
  title: string;
  sub?: string;
  onClose: () => void;
  // open is whether the sheet is raised. Closed, Sheet draws its children in the same two elements with no look of
  // their own, so a caller keeps them mounted in one place whether the sheet is raised or not.
  open?: boolean;
  children: ComponentChildren;
}

// Sheet draws its children in a sheet rising over a dimmed scrim, headed by its title, while it is open. The sheet takes
// focus when it opens. Escape inside it closes it and stops there, so the page's own key handler never sees that key,
// and a press on the scrim outside it closes it too.
export function Sheet({ title, sub, onClose, open = true, children }: SheetProps) {
  const panel = useRef<HTMLElement>(null);
  useEffect(() => {
    if (open) {
      panel.current?.focus();
    }
  }, [open]);
  const onKeyDown = (event: KeyboardEvent) => {
    if (open && event.key === "Escape") {
      event.stopPropagation();
      onClose();
    }
  };
  const onScrimPointerDown = (event: JSX.TargetedPointerEvent<HTMLDivElement>) => {
    if (open && event.target === event.currentTarget) {
      onClose();
    }
  };
  return (
    <div class={open ? "sheet-scrim" : "sheet-off"} onPointerDown={onScrimPointerDown}>
      <section
        ref={panel}
        class={open ? "sheet" : "sheet-off"}
        role={open ? "dialog" : undefined}
        aria-label={open ? title : undefined}
        tabIndex={open ? -1 : undefined}
        onKeyDown={onKeyDown}
      >
        {open && (
          <div class="sheet-head">
            <b>{title}</b>
            {sub}
          </div>
        )}
        {children}
      </section>
    </div>
  );
}
