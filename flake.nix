{
  description = "Cross-language CLI end-to-end test libraries";

  inputs.nixpkgs.url = "https://flakehub.com/f/NixOS/nixpkgs/0.1";

  outputs =
    {
      nixpkgs,
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
          };
          common = [ pkgs.just ];
          goTools = [ pkgs.go pkgs.golangci-lint ];
          typescriptTools = [ pkgs.nodejs_24 pkgs.pnpm ];
          rustTools = [ pkgs.cargo pkgs.rustc pkgs.rustfmt pkgs.clippy ];
          haskellTools = [
            (pkgs.haskellPackages.ghcWithPackages (p: with p; [
              aeson async bytestring containers directory filepath hspec
              http-client http-types network random tar text time
            ]))
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
