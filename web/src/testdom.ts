import { Window } from "happy-dom";

const globalNames = ["window", "document", "Node", "Element", "HTMLElement", "Event", "KeyboardEvent"] as const;

// installTestDom makes a fresh happy-dom window the global DOM for a component test and returns it.
export function installTestDom(): Window {
  const window = new Window({ url: "https://orrery.test/" });
  const target = globalThis as unknown as Record<string, unknown>;
  target.window = window;
  target.document = window.document;
  target.Node = window.Node;
  target.Element = window.Element;
  target.HTMLElement = window.HTMLElement;
  target.Event = window.Event;
  target.KeyboardEvent = window.KeyboardEvent;
  return window;
}

// removeTestDom removes the globals installTestDom set and closes the window.
export async function removeTestDom(window: Window): Promise<void> {
  const target = globalThis as unknown as Record<string, unknown>;
  for (const name of globalNames) {
    delete target[name];
  }
  await window.close();
}
