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
        platformVersions = [ "37.0" ];
      };
      androidSdk = "${androidComposition.androidsdk}/libexec/android-sdk";
      androidAapt2 = "${androidSdk}/build-tools/36.0.0/aapt2";
    in
    {
      packages = forAllSystems (
        pkgs:
        let
          webNodeModules = (import ./nix/web-node-modules.nix) pkgs;
        in
        {
          # The web page is bundled before the Go build and replaces the committed stub in the build's own copy of
          # internal/webui/dist, so the binary embeds the built page. scripts/build-web.sh is the one bundle command.
          default = pkgs.buildGoModule {
            pname = "orrery";
            version = "0.0.0";
            src = ./.;
            go = pkgs.go_1_26;
            vendorHash = "sha256-+S4TApzBDXfo7aV9CHoDsM7ZDN1HVG/LOR5YsshLipg=";
            subPackages = [
              "cmd/orrery"
              "cmd/orreryd"
            ];
            nativeBuildInputs = [ pkgs.esbuild ];
            nativeCheckInputs = [
              pkgs.tmux
              pkgs.util-linux
            ];
            preBuild = ''
              ln -s ${webNodeModules}/node_modules web/node_modules
              bash scripts/build-web.sh web/build/page
              cp -r web/build/page/. internal/webui/dist/
            '';
            preCheck = ''
              export TMUX_TMPDIR="$TMPDIR"
            '';
            # The default check covers only subPackages, so the webui tests run here against the embedded page.
            postCheck = ''
              go test ./internal/webui
            '';
          };
        }
      );

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
          withRoots = homeFor {
            package = orrery;
            roots = [
              {
                path = "Work";
                depth = 2;
              }
              {
                path = "Shared Projects";
                depth = 1;
              }
            ];
          };
          localRootsFile = local.config.xdg.configFile."orrery/roots".source or "";
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

            [ -z "${localRootsFile}" ] || fail "configuration without roots writes an orrery/roots file"
            printf '%s\n' '2 Work' '1 Shared Projects' > expectedRoots
            rootsFile=${withRoots.config.xdg.configFile."orrery/roots".source}
            cmp -s "$rootsFile" expectedRoots || fail "roots file is [$(cat "$rootsFile")], want [2 Work, 1 Shared Projects]"

            status=0
            "$p_remote" . 2>err || status=$?
            [ "$status" -eq 2 ] || fail "p . exited $status, want 2"
            [ -s err ] || fail "p . printed no refusal on stderr"
            [ ! -e "$SSH_LOG" ] || fail "p . reached ssh"

            touch $out
          '';

          # The binary must carry the built page's markers and not the committed stub, which go:embed would
          # otherwise ship when the preBuild copy into internal/webui/dist is missing.
          embeds-web-page = pkgs.runCommand "orrery-embeds-web-page-check" { } ''
            set -eu
            bin=${orrery}/bin/orrery
            fail() { echo "orrery embed check: $*" >&2; exit 1; }

            if grep -qF 'built without the web page' "$bin"; then
              fail "$bin embeds the committed stub internal/webui/dist/index.html, not the built page"
            fi
            grep -qF '<script type="module" src="main.js"></script>' "$bin" \
              || fail "$bin lacks the bundle's entry script tag"
            grep -qF '<div id="app"></div>' "$bin" \
              || fail "$bin lacks the bundle's app mount point"

            touch $out
          '';
        }
      );

      devShells.x86_64-linux.android = androidPkgs.mkShell {
        packages = [
          androidComposition.androidsdk
          androidPkgs.gradle_9
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
