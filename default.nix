{ lib, buildGoModule }:
buildGoModule (finalAttrs: {
  pname = "snot";
  version = "0.1.0";

  src = ./.;

  vendorHash = "sha256-uoHU3nySdQ/h1M9MWyrW/9h963byGV5AYvzW2xPkXyE=";

  subPackages = [ "cmd/snot" ];

  ldflags = [
    "-s"
    "-w"
    "-X main.Version=${finalAttrs.version}"
    "-X github.com/isabelroses/snot/internal/xrpc.version=${finalAttrs.version}"
  ];

  meta = {
    description = "A tangled knot shim backed by a Forgejo instance";
    homepage = "https://github.com/isabelroses/snot";
    license = lib.licenses.mit;
    maintainers = with lib.maintainers; [ isabelroses ];
    mainProgram = "snot";
  };
})
