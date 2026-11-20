import type { ComponentChildren, JSX } from "preact";
import { useEffect, useRef } from "preact/hooks";

export interface SheetProps {
  title: string;
  sub?: string;
  onClose: () => void;
  children: ComponentChildren;
}

// Sheet draws its children in a sheet rising over a dimmed scrim, headed by its title. The sheet takes focus when it
// mounts. Escape inside it closes it and stops there, so the page's own key handler never sees that key, and a press on
// the scrim outside it closes it too.
export function Sheet({ title, sub, onClose, children }: SheetProps) {
  const panel = useRef<HTMLElement>(null);
  useEffect(() => {
    panel.current?.focus();
  }, []);
  const onKeyDown = (event: KeyboardEvent) => {
    if (event.key === "Escape") {
      event.stopPropagation();
      onClose();
    }
  };
  const onScrimPointerDown = (event: JSX.TargetedPointerEvent<HTMLDivElement>) => {
    if (event.target === event.currentTarget) {
      onClose();
    }
  };
  return (
    <div class="sheet-scrim" onPointerDown={onScrimPointerDown}>
      <section ref={panel} class="sheet" role="dialog" aria-label={title} tabIndex={-1} onKeyDown={onKeyDown}>
        <div class="sheet-head">
          <b>{title}</b>
          {sub}
        </div>
        {children}
      </section>
    </div>
  );
}
