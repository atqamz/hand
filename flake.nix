{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
  };

  outputs = { nixpkgs, ... }:
    let
      version = "0.1.0"; # x-release-please-version
      systems = [ "aarch64-darwin" "x86_64-darwin" "aarch64-linux" "x86_64-linux" ];
      forAllSystems = nixpkgs.lib.genAttrs systems;
    in {
      packages = forAllSystems (system:
        let pkgs = nixpkgs.legacyPackages.${system}; in {
          default = pkgs.buildGoModule {
            pname = "secondhand";
            inherit version;
            src = ./.;
            vendorHash = null; # update after first go mod tidy
            ldflags = [ "-s" "-w" "-X main.version=v${version}" ];
          };
        }
      );
    };
}
