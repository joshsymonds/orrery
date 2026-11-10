{
  description = "orrery: tmux pane state for Claude Code sessions";

  # devenv.nix/devenv.yaml own the Go dev shell. This flake and devenv.yaml pin
  # the SAME literal nixpkgs rev, so both share one toolchain. Move both together.
  inputs.nixpkgs.url = "github:NixOS/nixpkgs/02c087f3fe673c9e317f206a627cb6b910336dbe";
  inputs.home-manager = {
    url = "github:nix-community/home-manager";
    inputs.nixpkgs.follows = "nixpkgs";
  };

  outputs =
    {
      self,
      nixpkgs,
      home-manager,
    }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "aarch64-darwin"
      ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});

      # The Android SDK is unfree, so the shell needs its own pkgs set with the
      # licence accepted. The SDK comes without an emulator: the unit tests and
      # the debug APK need only the platform, build tools, and platform-tools.
      androidPkgs = import nixpkgs {
        system = "x86_64-linux";
        config = {
          allowUnfree = true;
          android_sdk.accept_license = true;
        };
      };
      androidComposition = androidPkgs.androidenv.composeAndroidPackages {
        cmdLineToolsVersion = "20.0";
        platformToolsVersion = "37.0.1";
        buildToolsVersions = [ "36.0.0" ];
        platformVersions = [ "36" ];
      };
      androidSdk = "${androidComposition.androidsdk}/libexec/android-sdk";
      androidAapt2 = "${androidSdk}/build-tools/36.0.0/aapt2";
    in
    {
      packages = forAllSystems (pkgs: {
        default = pkgs.buildGoModule {
          pname = "orrery";
          version = "0.0.0";
          src = ./.;
          go = pkgs.go_1_26;
          vendorHash = "sha256-GJLFZqDYsRsCeghybw54VR3MYkv/zCsFAOHpzwXr38E=";
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

      homeManagerModules.default = import ./nix/home-manager.nix { inherit self; };

      checks = forAllSystems (
        pkgs:
        let
          system = pkgs.stdenv.hostPlatform.system;
          orrery = self.packages.${system}.default;
          # Stands in for the package where `open` is observed. It prints its own path, then its argv.
          argvStub = pkgs.writeShellScriptBin "orrery" ''
            printf '%s\n' "$0" "$@"
          '';
          homeFor =
            orreryConfig:
            home-manager.lib.homeManagerConfiguration {
              inherit pkgs;
              modules = [
                self.homeManagerModules.default
                {
                  home.username = "example";
                  home.homeDirectory = "/home/example";
                  home.stateVersion = "25.05";
                  programs.tmux.enable = true;
                  programs.orrery = {
                    enable = true;
                  }
                  // orreryConfig;
                }
              ];
            };
          local = homeFor { package = orrery; };
          stubLocal = homeFor { package = argvStub; };
          stubRemote = homeFor {
            package = argvStub;
            remoteHost = "example-host";
          };
        in
        {
          package = self.packages.${system}.default;

          home-manager = pkgs.runCommand "orrery-home-manager-check" { } ''
            set -eu
            fail() { echo "orrery home-manager check: $*" >&2; exit 1; }

            tmuxConf=${local.config.xdg.configFile."tmux/tmux.conf".source}
            sourced=$(sed -n 's/^source-file //p' "$tmuxConf")
            [ -n "$sourced" ] || fail "generated tmux.conf sources no file"
            grep -qF "${orrery}/bin/orrery tree --client" "$sourced" \
              || fail "sourced file $sourced lacks ${orrery}/bin/orrery tree --client"

            [ -x ${local.config.home.path}/bin/orrery ] || fail "profile lacks orrery"
            [ -x ${local.config.home.path}/bin/p ] || fail "profile lacks p"

            localOut=$(${stubLocal.config.home.path}/bin/p two words "a b")
            expected=$(printf '%s\n' "${argvStub}/bin/orrery" open two words "a b")
            [ "$localOut" = "$expected" ] || fail "local p printed [$localOut], want [$expected]"

            fakebin=$PWD/fakebin
            mkdir -p "$fakebin"
            cat > "$fakebin/ssh" <<'EOF'
            #!/bin/sh
            printf '%s\n' "$@" > "$SSH_LOG"
            EOF
            chmod +x "$fakebin/ssh"
            export SSH_LOG=$PWD/ssh.log
            export PATH=$fakebin:$PATH

            p_remote=${stubRemote.config.home.path}/bin/p
            "$p_remote" two words "a b"
            [ "$(wc -l < "$SSH_LOG")" -eq 3 ] || fail "ssh argv is [$(cat "$SSH_LOG")], want 3 arguments"
            [ "$(sed -n 1p "$SSH_LOG")" = -t ] || fail "ssh first argument is not -t"
            [ "$(sed -n 2p "$SSH_LOG")" = example-host ] || fail "ssh host is not example-host"
            remoteCmd=$(sed -n 3p "$SSH_LOG")
            remoteOut=$(PATH=${argvStub}/bin:$PATH sh -c "$remoteCmd")
            expected=$(printf '%s\n' "${argvStub}/bin/orrery" open --remote two words "a b")
            [ "$remoteOut" = "$expected" ] || fail "remote command printed [$remoteOut], want [$expected]"

            rm -f "$SSH_LOG"
            status=0
            "$p_remote" . 2>err || status=$?
            [ "$status" -eq 2 ] || fail "p . exited $status, want 2"
            [ -s err ] || fail "p . printed no refusal on stderr"
            [ ! -e "$SSH_LOG" ] || fail "p . reached ssh"

            touch $out
          '';
        }
      );

      devShells.x86_64-linux.android = androidPkgs.mkShell {
        packages = [
          androidComposition.androidsdk
          androidPkgs.gradle
          androidPkgs.jdk17
        ];
        ANDROID_HOME = androidSdk;
        ANDROID_SDK_ROOT = androidSdk;
        JAVA_HOME = "${androidPkgs.jdk17}";
        # NixOS has no FHS aapt2, so Gradle must use the SDK's copy.
        GRADLE_OPTS = "-Dandroid.aapt2FromMavenOverride=${androidAapt2} -Dorg.gradle.project.android.aapt2FromMavenOverride=${androidAapt2}";
      };
    };
}
