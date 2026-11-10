{ pkgs, config, ... }:

# The orrery Go dev shell. devenv.nix imports this module.

{
  packages = [
    pkgs.buf
    pkgs.delve
    pkgs.git
    pkgs.go_1_26
    pkgs.golangci-lint
    pkgs.goose
    pkgs.gopls
    pkgs.jq
    pkgs.just
    pkgs.procps
    pkgs.protoc-gen-connect-go
    pkgs.protoc-gen-go
    pkgs.sqlc
    pkgs.tmux
    pkgs.util-linux
  ];

  # Go's module and build caches are shared across worktrees under a common
  # path, so a new worktree does not redownload modules or rebuild the cache.
  env.GOMODCACHE = "${builtins.getEnv "HOME"}/.cache/orrery/mod";
  env.GOCACHE = "${builtins.getEnv "HOME"}/.cache/orrery/build";

  # golangci-lint's cache keys analysis results by package import path, which
  # is identical across worktrees. A shared cache would replay one worktree's
  # results and file paths inside another, so keep it per checkout.
  env.GOLANGCI_LINT_CACHE = "${config.devenv.root}/.golangci-cache";

  # `nix develop` exported SHELL as bash. Tests that attach tmux clients through
  # script(1) run their command with $SHELL, so keep it bash rather than the
  # caller's login shell. devenv drops SHELL from env.*, so export it here.
  enterShell = ''
    mkdir -p "$GOMODCACHE" "$GOCACHE" "$GOLANGCI_LINT_CACHE"
    export SHELL=${pkgs.bashInteractive}/bin/bash
  '';
}
