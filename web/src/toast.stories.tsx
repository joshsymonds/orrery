// The story of Toast: one line that says an archive happened, with its Undo action.

import * as toast from "./toast";
import type { Stories } from "./catalog/story";

export const Toast: Stories = {
  "with-undo": () => (
    <toast.Toast text="Archived “Tide table cache warm-up”." action="Undo" onAction={() => {}} />
  ),
};
