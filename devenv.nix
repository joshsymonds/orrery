{ ... }:

{
  imports = [
    ./devenv-base.nix
  ];

  profiles.tui-shots.module = ./tui-shots.nix;
  profiles.visual.module = ./visual.nix;
}
