{
  description = "orrery: tmux pane state for Claude Code sessions";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs =
    { self, nixpkgs }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "aarch64-darwin"
      ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
    in
    {
      packages = forAllSystems (pkgs: {
        default = pkgs.buildGoModule {
          pname = "orrery";
          version = "0.0.0";
          src = ./.;
          go = pkgs.go_1_26;
          vendorHash = "sha256-Qc+8NFF8weEl0jXtk0LKqErGleQU6mxLNKlcNqazI+w=";
          subPackages = [
            "cmd/orrery"
            "cmd/orreryd"
          ];
          nativeCheckInputs = [
            pkgs.tmux
            pkgs.util-linux
          ];
          preCheck = ''
            export TMUX_TMPDIR="$TMPDIR"
          '';
        };
      });

      checks = forAllSystems (pkgs: {
        package = self.packages.${pkgs.stdenv.hostPlatform.system}.default;
      });

      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShell {
          packages = [
            pkgs.go_1_26
            pkgs.golangci-lint
            pkgs.just
            pkgs.tmux
            pkgs.util-linux
          ];
        };
      });
    };
}
