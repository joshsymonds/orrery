{ pkgs, config, ... }:

# The orrery Go dev shell. devenv.nix imports this module.

let
  # Connect-Kotlin's generator, pinned to its Maven Central jar. The jar is self-contained, and the wrapper runs it
  # on a pinned headless JDK so the generator never picks up the caller's Java.
  protocGenConnectKotlinJar = pkgs.fetchurl {
    url = "https://repo1.maven.org/maven2/com/connectrpc/protoc-gen-connect-kotlin/0.10.0/protoc-gen-connect-kotlin-0.10.0.jar";
    hash = "sha256-MNFmuoe2yaTfpQGiJB8jsMZSOWCCinmLzkUpSYjMxls=";
  };
  protoc-gen-connect-kotlin = pkgs.writeShellScriptBin "protoc-gen-connect-kotlin" ''
    exec ${pkgs.jdk17_headless}/bin/java -jar ${protocGenConnectKotlinJar} "$@"
  '';
  webNodeModules = (import ./nix/web-node-modules.nix) pkgs;
  linkNodeModules = "${pkgs.path}/pkgs/build-support/node/import-npm-lock/hooks/link-node-modules.js";
in

{
  packages = [
    pkgs.buf
    pkgs.delve
    pkgs.esbuild
    pkgs.git
    pkgs.go_1_26
    pkgs.golangci-lint
    pkgs.goose
    pkgs.gopls
    pkgs.jq
    pkgs.just
    pkgs.nodejs
    pkgs.procps
    pkgs.protobuf
    pkgs.protoc-gen-connect-go
    pkgs.protoc-gen-es
    pkgs.protoc-gen-go
    pkgs.sqlc
    pkgs.tmux
    pkgs.typescript
    pkgs.util-linux
    protoc-gen-connect-kotlin
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
  #
  # web/node_modules is a directory of symlinks into webNodeModules, linked the way importNpmLock's
  # linkNodeModulesHook links it. Entering the shell rebuilds the links, so a changed lockfile is picked up.
  enterShell = ''
    mkdir -p "$GOMODCACHE" "$GOCACHE" "$GOLANGCI_LINT_CACHE"
    export SHELL=${pkgs.bashInteractive}/bin/bash
    (cd "${config.devenv.root}/web" && ${pkgs.nodejs}/bin/node ${linkNodeModules} ${builtins.storeDir} ${webNodeModules}/node_modules)
  '';
}
