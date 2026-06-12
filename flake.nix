{
  description = "tangled knot shim backed by Forgejo";

  inputs = {
    nixpkgs.url = "https://channels.nixos.org/nixpkgs-unstable/nixexprs.tar.xz";
  };

  outputs =
    { self, nixpkgs }:
    let
      forAllSystems =
        function:
        nixpkgs.lib.genAttrs nixpkgs.lib.systems.flakeExposed (
          system: function nixpkgs.legacyPackages.${system}
        );
    in
    {
      packages = forAllSystems (pkgs: {
        snot = pkgs.callPackage ./default.nix { };
        default = self.packages.${pkgs.stdenv.hostPlatform.system}.snot;
      });

      devShells = forAllSystems (pkgs: {
        default = pkgs.callPackage ./shell.nix { };
      });

      overlays.default = final: _: { snot = final.callPackage ./default.nix { }; };

      nixosModules.default = { pkgs, ... }: {
        _class = "nixos";
        _file = "${self.outPath}/flake.nix#nixosModules.default";
        imports = [ ./nix/module.nix ];
        config.services.snot.package =
          self.packages.${pkgs.stdenv.hostPlatform.system}.default;
      };
    };
}
