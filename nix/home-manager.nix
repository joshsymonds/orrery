{ self }:
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.programs.orrery;

  tmuxConf = pkgs.runCommand "orrery-tmux.conf" { } ''
    ${cfg.package}/bin/orrery tmux-conf > $out
  '';

  p = pkgs.writeShellScriptBin "p" (
    if cfg.remoteHost == null then
      ''
        exec ${cfg.package}/bin/orrery open "$@"
      ''
    else
      ''
        if [ "$#" -eq 1 ] && [ "$1" = . ]; then
          echo "p: . is local only; run it on the host itself" >&2
          exit 2
        fi
        remote=""
        for arg in "$@"; do
          remote="$remote $(printf '%q' "$arg")"
        done
        exec ssh -t ${lib.escapeShellArg cfg.remoteHost} "orrery open --remote$remote"
      ''
  );
in
{
  options.programs.orrery = {
    enable = lib.mkEnableOption "orrery, tmux pane state for Claude Code sessions";

    package = lib.mkOption {
      type = lib.types.package;
      default = self.packages.${pkgs.stdenv.hostPlatform.system}.default;
      defaultText = lib.literalExpression "orrery's packages.<system>.default";
      description = "The orrery package to install and run.";
    };

    remoteHost = lib.mkOption {
      type = lib.types.nullOr lib.types.str;
      default = null;
      example = "dev-box";
      description = ''
        SSH host that `p` opens sessions on, through `orrery open --remote`.
        When null, `p` runs `orrery open` locally.
      '';
    };

    roots = lib.mkOption {
      type = lib.types.listOf (
        lib.types.submodule {
          options = {
            path = lib.mkOption {
              type = lib.types.str;
              example = "Work";
              description = "Directory relative to the home directory.";
            };

            depth = lib.mkOption {
              type = lib.types.ints.positive;
              example = 2;
              description = "Levels of subdirectories below `path` that the intent picker lists.";
            };
          };
        }
      );
      default = [ ];
      example = [
        {
          path = "Work";
          depth = 2;
        }
      ];
      description = ''
        Directory trees the intent picker lists. For each root, `p` lists its
        subdirectories down to `depth` levels, after the directories `orrery`
        already knows from its sessions, archive and store, and the children
        of `~/Personal`, capped at 300 entries. Written to
        `$XDG_CONFIG_HOME/orrery/roots` (or `~/.config/orrery/roots`), which
        `orrery open` and the tree's `N` read. Empty writes no roots file.
      '';
    };
  };

  config = lib.mkIf cfg.enable {
    home.packages = [
      cfg.package
      p
    ];

    programs.tmux.extraConfig = "source-file ${tmuxConf}";

    xdg.configFile."orrery/roots" = lib.mkIf (cfg.roots != [ ]) {
      text = lib.concatMapStrings (root: "${toString root.depth} ${root.path}\n") cfg.roots;
    };
  };
}
