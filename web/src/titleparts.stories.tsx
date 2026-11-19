// The stories of TitleParts: a title with a directory and a line, a title with only a line, one with only a directory,
// and a line that holds a link.

import type { Stories } from "./catalog/story";
import * as titleparts from "./titleparts";

export const TitleParts: Stories = {
  "dir-and-line": () => (
    <titleparts.TitleParts dir="/home/owner/code/lighthouse" line="adding retry backoff" />
  ),
  "line-only": () => <titleparts.TitleParts dir="" line="waits on you" />,
  "dir-only": () => <titleparts.TitleParts dir="/home/owner/code/tidepool" line="" />,
  "link-in-line": () => (
    <titleparts.TitleParts dir="/home/owner/code/orchard" line="graft notes at https://example.com/orchard/graft" />
  ),
};
