{
  lib,
  buildGoModule,
  gitMinimal,
}:
buildGoModule (finalAttrs: {
  pname = "snot";
  version = "0.1.0";

  src = ./.;

  vendorHash = "sha256-GeoCWP8azDeQu+acSlmzjf0Adogq/WlbQirB2yma9Tk=";

  ldflags = [
    "-s"
    "-w"
    "-X main.Version=${finalAttrs.version}"
    "-X github.com/isabelroses/snot/internal/xrpc.version=${finalAttrs.version}"
  ];

  nativeCheckInputs = [ gitMinimal ];

  meta = {
    description = "A tangled knot shim backed by a Forgejo instance";
    homepage = "https://github.com/isabelroses/snot";
    license = lib.licenses.mit;
    maintainers = with lib.maintainers; [ isabelroses ];
    mainProgram = "snot";
  };
})
