// The shape of a story catalog: a story draws one state of a component, and a catalog holds the stories of one module's
// exports, each record keyed by a state. A story's name is its export's name in kebab case, a slash and its state.

import type { VNode } from "preact";

// Story draws one state of a component.
export type Story = () => VNode;

// Stories is the record of one export's states, each keyed by its state's name.
export type Stories = Readonly<Record<string, Story>>;

// kebab writes an export's name in kebab case: InboxRow as inbox-row, and App as app.
export function kebab(exportName: string): string {
  return exportName.replace(/([a-z0-9])([A-Z])/g, "$1-$2").toLowerCase();
}

// storyName is the name a story of exportName's state takes in the catalog: the export's kebab-case name, a slash, and
// the state.
export function storyName(exportName: string, state: string): string {
  return `${kebab(exportName)}/${state}`;
}
