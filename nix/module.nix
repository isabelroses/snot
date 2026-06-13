{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.services.snot;

  inherit (lib)
    getExe
    mkEnableOption
    mkIf
    mkOption
    mkPackageOption
    ;

  settingsFormat = pkgs.formats.toml { };
  configFile = settingsFormat.generate "snot.toml" cfg.settings;
in
{
  options.services.snot = {
    enable = mkEnableOption "snot, a tangled knot backed by Forgejo";

    package = mkPackageOption pkgs "snot" { };

    settings = mkOption {
      type = settingsFormat.type;
      default = { };
      example = {
        hostname = "knot.example.com";
        owner_did = "did:plc:abc123";
        repo_root = "/var/lib/forgejo/repositories";
        users."did:plc:abc123" = "isabel";
      };
      description = ''
        The configuration for snot.
      '';
    };

    forgejoGroup = mkOption {
      type = lib.types.str;
      default = "forgejo";
      description = "Group with read access to the repository root.";
    };

    environmentFiles = mkOption {
      type = lib.types.listOf lib.types.path;
      default = [ ];
      description = ''
        Files to load environment variables from. `SNOT_*` variables override
        the corresponding TOML keys; use them for secrets such as a `db_dsn`
        with a password.
      '';
    };
  };

  config = mkIf cfg.enable {
    systemd.services.snot = {
      description = "tangled knot shim backed by Forgejo";
      wantedBy = [ "multi-user.target" ];
      after = [
        "network.target"
        "postgresql.service"
      ];
      path = [ pkgs.git ];

      serviceConfig = {
        ExecStart = "${getExe cfg.package} serve --config ${configFile}";
        EnvironmentFile = cfg.environmentFiles;

        DynamicUser = true;
        SupplementaryGroups = [ cfg.forgejoGroup ];
        StateDirectory = "snot";
        StateDirectoryMode = "0700";

        Restart = "on-failure";

        # hardening
        NoNewPrivileges = true;
        PrivateTmp = true;
        PrivateDevices = true;
        ProtectSystem = "strict";
        ProtectHome = true;
        ReadOnlyPaths = [ (cfg.settings.repo_root or "/var/lib/forgejo/repositories") ];
        ProtectKernelTunables = true;
        ProtectKernelModules = true;
        ProtectControlGroups = true;
        RestrictAddressFamilies = [
          "AF_UNIX"
          "AF_INET"
          "AF_INET6"
        ];
        RestrictNamespaces = true;
        LockPersonality = true;
        MemoryDenyWriteExecute = true;
        RestrictRealtime = true;
        SystemCallArchitectures = "native";
        SystemCallFilter = [ "@system-service" ];
      };
    };
  };
}
