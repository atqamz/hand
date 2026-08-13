{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
  };

  outputs = { nixpkgs, ... }:
    let
      version = "0.3.0"; # x-release-please-version
      # x86_64-darwin is excluded: the pinned nixpkgs-unstable aborts evaluation
      # for it since upstream dropped support, breaking every output.
      systems = [ "aarch64-darwin" "aarch64-linux" "x86_64-linux" ];
      forAllSystems = nixpkgs.lib.genAttrs systems;
    in {
      packages = forAllSystems (system:
        let pkgs = nixpkgs.legacyPackages.${system}; in {
          default = pkgs.buildGoModule {
            pname = "hand";
            inherit version;
            src = ./.;
            vendorHash = "sha256-v84XwEQIPE8kqHDOhWcOS9pwk+ebjRJ/3XFuuwa0+aU=";
            ldflags = [ "-s" "-w" "-X main.version=v${version}" "-X main.channel=dev" "-X main.commit=" ];
            nativeCheckInputs = [ pkgs.git ]; # test suite execs git directly
            meta.mainProgram = "hand";
          };
        }
      );

      devShells = forAllSystems (system:
        let pkgs = nixpkgs.legacyPackages.${system}; in {
          default = pkgs.mkShell {
            packages = [ pkgs.go pkgs.golangci-lint pkgs.gopls pkgs.gotools pkgs.gcc ];
          };
        }
      );
    };
}
