{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
  };

  outputs = { nixpkgs, ... }:
    let
      version = "0.1.2"; # x-release-please-version
      # x86_64-darwin is excluded: the pinned nixpkgs-unstable aborts evaluation
      # for it since upstream dropped support, breaking every output.
      systems = [ "aarch64-darwin" "aarch64-linux" "x86_64-linux" ];
      forAllSystems = nixpkgs.lib.genAttrs systems;
    in {
      packages = forAllSystems (system:
        let pkgs = nixpkgs.legacyPackages.${system}; in {
          default = pkgs.buildGoModule {
            pname = "secondhand";
            inherit version;
            src = ./.;
            vendorHash = "sha256-7K17JaXFsjf163g5PXCb5ng2gYdotnZ2IDKk8KFjNj0=";
            ldflags = [ "-s" "-w" "-X main.version=v${version}" ];
            nativeCheckInputs = [ pkgs.git ]; # test suite execs git directly
            # buildGoModule names the output after the module (secondhand);
            # every other build path (Makefile, .gitignore) expects hand.
            postInstall = "mv $out/bin/secondhand $out/bin/hand";
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
