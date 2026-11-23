// The held layout's shared types: the thing a desk holds open beside its conversation, and the motion between two holds.

// HeldKind is the kind of a thing held open: a design, a contract or an epic.
export type HeldKind = "design" | "contract" | "epic";

// HeldThing is a thing held open. key is its tree key: design:<id>, contract:<id> or epic:<id>. convo is the tree key of the
// conversation that holds it, or "" when no conversation holds it.
export interface HeldThing { kind: HeldKind; key: string; convo: string }

// HeldMove is the motion a change of the held thing draws: "" for none, "open" for a thing held where none was, "switch"
// for another thing in the same conversation, "other" for a thing in another conversation, and "close" for a thing let go.
export type HeldMove = "" | "open" | "switch" | "other" | "close";

// heldMove says which HeldMove a change from before to after draws. The motion task will make it return open from null to a
// thing; close from a thing to null; switch between two keys with the same convo; other between different convos; and ""
// for the same key, or null to null. This stub returns "" for every change.
export function heldMove(before: HeldThing | null, after: HeldThing | null): HeldMove {
  return "";
}
