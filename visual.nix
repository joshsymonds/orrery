{ pkgs, ... }:

# The runtime of the web catalog's screenshots. The profile carries the nixpkgs Chromium that playwright-core drives for
# screenshots of the web catalog, at ORRERY_VISUAL_CHROMIUM, rather than a browser it downloads. It loads only with
# `devenv --profile visual`, so the default shell and the gates stay free of it.

{
  packages = [
    pkgs.chromium
  ];

  env.ORRERY_VISUAL_CHROMIUM = "${pkgs.chromium}/bin/chromium";
}
