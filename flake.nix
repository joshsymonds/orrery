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
          vendorHash = null;
          subPackages = [
            "cmd/orrery"
            "cmd/orreryd"
          ];
          nativeCheckInputs = [ pkgs.tmux ];
          preCheck = ''
            export TMUX_TMPDIR="$TMPDIR"
          '';
        };
      });

      checks = forAllSystems (pkgs: {
        package = self.packages.${pkgs.stdenv.hostPlatform.system}.default;
      });

      devShells = nixpkgs.lib.recursiveUpdate
        (forAllSystems (pkgs: {
          default = pkgs.mkShell {
            packages = [
              pkgs.go_1_26
              pkgs.golangci-lint
              pkgs.just
              pkgs.tmux
            ];
          };
        }))
        {
          x86_64-linux.android = androidPkgs.mkShell {
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
    };
}
