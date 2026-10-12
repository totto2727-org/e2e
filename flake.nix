{
  description = "Cross-language CLI end-to-end test libraries";

  inputs = {
    nixpkgs.url = "https://flakehub.com/f/NixOS/nixpkgs/0.1";
    vite-plus-overlay = {
      url = "github:ryoppippi/nix-vite-plus";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs =
    {
      nixpkgs,
      vite-plus-overlay,
      ...
    }:
    let
      supportedSystems = [
        "aarch64-darwin"
        "aarch64-linux"
        "x86_64-linux"
      ];
      forEachSystem = nixpkgs.lib.genAttrs supportedSystems;
    in
    {
      devShells = forEachSystem (
        system:
        let
          pkgs = import nixpkgs {
            inherit system;
            overlays = [ vite-plus-overlay.overlays.default ];
          };
          common = [
            pkgs.just
            pkgs.nixfmt
          ];
          goTools = [
            pkgs.go
            pkgs.golangci-lint
          ];
          typescriptTools = [
            pkgs.nodejs_24
            pkgs.pnpm
            pkgs.vite-plus
          ];
          rustTools = [
            pkgs.cargo
            pkgs.rustc
            pkgs.rustfmt
            pkgs.clippy
          ];
          haskellTools = [
            (pkgs.haskellPackages.ghcWithPackages (
              p: with p; [
                aeson
                async
                bytestring
                containers
                filepath
                hspec
                http-client
                http-types
                network
                tar
                text
              ]
            ))
            pkgs.cabal-install
          ];
        in
        {
          default = pkgs.mkShell {
            packages = common ++ goTools ++ typescriptTools ++ rustTools ++ haskellTools;
          };
          go = pkgs.mkShell { packages = common ++ goTools; };
          typescript = pkgs.mkShell { packages = common ++ typescriptTools; };
          rust = pkgs.mkShell { packages = common ++ rustTools; };
          haskell = pkgs.mkShell { packages = common ++ haskellTools; };
        }
      );
    };
}
