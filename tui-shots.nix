{ pkgs, ... }:

# VHS screenshot tooling for `just tui-shots`. It loads only with
# `devenv --profile tui-shots`, so the default shell and the snapshot used by
# every gate stay free of the vhs, ttyd and ffmpeg closure.
#
# Two runtime notes the `just tui-shots` recipe depends on:
#   - VHS's headless renderer needs its font on the fontconfig path;
#     ORRERY_TUI_FONT_DIR points at Maple Mono NF's font directory so the
#     recipe can register it with fontconfig.
#   - ttyd fails to start unless its libwebsockets evlib_uv plugin is on the
#     library path; the recipe derives that dir from ttyd's own linkage.

{
  packages = [
    pkgs.vhs
    pkgs.ttyd
    pkgs.ffmpeg
    pkgs.fontconfig
    pkgs.maple-mono.NF
  ];

  env.ORRERY_TUI_FONT_DIR = "${pkgs.maple-mono.NF}/share/fonts";
}
