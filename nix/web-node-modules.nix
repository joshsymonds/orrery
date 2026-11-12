# The web client's node_modules, built by nix from web/package-lock.json. npm rebuild gets --ignore-scripts, so no
# package's install script runs and the locked package list is all that enters the build.
pkgs:

pkgs.importNpmLock.buildNodeModules {
  npmRoot = ../web;
  nodejs = pkgs.nodejs;
  derivationArgs.npmRebuildFlags = [ "--ignore-scripts" ];
}
