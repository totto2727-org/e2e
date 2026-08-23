{
  description = "Reusable Go CLI end-to-end test helpers";

  inputs.nixpkgs.url = "https://flakehub.com/f/NixOS/nixpkgs/0.1";

  outputs =
    {
      self,
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
        in
        {
          default = pkgs.mkShell {
            packages = [
              pkgs.go
              pkgs.golangci-lint
              pkgs.just
            ];
          };
        }
      );

      packages = forEachSystem (system: let
        pkgs = import nixpkgs {
          inherit system;
        };
      in {
          default = pkgs.writeShellApplication {
            name = "e2e-example";
            runtimeInputs = [
              pkgs.docker
              pkgs.go
            ];
            text = ''
              docker build --tag e2e-example:local ${self}/example
              cd ${self}/example
              exec go test -race -v -shuffle=on -count=1 -parallel=2 ./...
            '';
          };
        });

      apps = forEachSystem (system: {
        default = {
          type = "app";
          program = "${self.packages.${system}.default}/bin/e2e-example";
          meta.description = "Run the checked-in Docker CLI E2E example";
        };
      });
    };
}
