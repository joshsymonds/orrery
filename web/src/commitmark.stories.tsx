// The story of CommitMark: a commit drawn as its ⎇ mark, with the full id in its hover text and label.

import * as commitmark from "./commitmark";
import type { Stories } from "./catalog/story";

export const CommitMark: Stories = {
  default: () => <commitmark.CommitMark hash="3e1f2ab6b681479f7e6dc3c139194a1ad6b8025b" />,
};
