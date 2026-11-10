{ ... }:

{
  imports = [
    ./devenv-base.nix
    ./githooks.nix
  ];

  profiles.tui-shots.module = ./tui-shots.nix;
}
