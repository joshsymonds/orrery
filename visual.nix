{ pkgs, ... }:

# The runtime of the visual check, for `just visual-report`. It loads only with `devenv --profile visual`, so the default
# shell and the snapshot used by every gate stay free of Chromium and the font.
#
# Two notes the visual recipes depend on:
#   - playwright-core drives the Chromium at ORRERY_VISUAL_CHROMIUM, the nixpkgs browser, not one it downloads.
#   - The wireframe's prose face, Atkinson Hyperlegible Next, comes from the fontconfig file this profile sets, by family
#     name: the wireframe's Google Fonts link is blocked by the checker, so the face has to come from the profile.

let
  fonts = pkgs.makeFontsConf { fontDirectories = [ pkgs.atkinson-hyperlegible-next ]; };
in

{
  packages = [
    pkgs.chromium
    pkgs.atkinson-hyperlegible-next
  ];

  env.ORRERY_VISUAL_CHROMIUM = "${pkgs.chromium}/bin/chromium";
  env.FONTCONFIG_FILE = "${fonts}";
}
