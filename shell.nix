{
  mkShell,
  callPackage,

  # extra tooling
  go,
  gopls,
  goreleaser,
}:
let
  defaultPackage = callPackage ./default.nix { };
in
mkShell {
  inputsFrom = [ defaultPackage ];

  packages = [
    go
    gopls
    goreleaser
  ];
}
