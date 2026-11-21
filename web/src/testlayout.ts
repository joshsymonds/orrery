// placeRect makes the element report edges as its rectangle, so a placement reads the layout the test sets.
export function placeRect(element: Element, edges: { top: number; bottom: number; right: number }): void {
  const rect = {
    x: 0,
    y: edges.top,
    width: edges.right,
    height: edges.bottom - edges.top,
    top: edges.top,
    bottom: edges.bottom,
    left: 0,
    right: edges.right,
    toJSON: () => ({}),
  };
  Object.assign(element, { getBoundingClientRect: () => rect });
}

// installResizeObserver replaces globalThis.ResizeObserver with a fake that keeps each callback it is built with in
// resized, so a test runs them to report a resize. restore puts the previous ResizeObserver back, and a test calls it
// when it ends.
export function installResizeObserver(): { resized: (() => void)[]; restore: () => void } {
  const resized: (() => void)[] = [];
  class FakeResizeObserver {
    constructor(callback: () => void) {
      resized.push(callback);
    }
    observe(): void {}
    disconnect(): void {}
    unobserve(): void {}
  }
  const previous = globalThis.ResizeObserver;
  globalThis.ResizeObserver = FakeResizeObserver as unknown as typeof ResizeObserver;
  return {
    resized,
    restore: () => {
      globalThis.ResizeObserver = previous;
    },
  };
}
