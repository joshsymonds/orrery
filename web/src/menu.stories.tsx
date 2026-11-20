// The story of Menu: a row's menu as round 6 draws it, open. Its items are Open in a new window, Mark seen and Archive.

import type { Stories } from "./catalog/story";
import { Menu as MenuView } from "./menu";

const noop = () => {};

export const Menu: Stories = {
  open: () => (
    <MenuView
      items={[
        { label: "Open in a new window", icon: "open", onSelect: noop },
        { label: "Mark seen", icon: "check", kbd: "x", onSelect: noop },
        "sep",
        { label: "Archive", icon: "archive", kbd: "A", onSelect: noop },
      ]}
    />
  ),
};
