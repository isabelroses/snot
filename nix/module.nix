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
    types
    ;
in
{
  options.services.snot = {
    enable = mkEnableOption "snot, a tangled knot backed by Forgejo";

    package = mkPackageOption pkgs "snot" { };

    settings = mkOption {
      description = ''
        Environment variables to set for the service. Secrets should be
        specified using {option}`environmentFiles`.
      '';
      type = types.submodule {
        freeformType = types.attrsOf (types.nullOr types.str);
        options = {
          SNOT_HOSTNAME = mkOption {
            type = types.str;
            example = "knot.example.com";
            description = "Public hostname of the shim (the knot domain).";
          };

          SNOT_LISTEN_ADDR = mkOption {
            type = types.str;
            default = "0.0.0.0:5555";
            description = "Address for the shim to listen on.";
          };

          SNOT_OWNER_DID = mkOption {
            type = types.str;
            example = "did:plc:abc123";
            description = "atproto DID of the knot owner; governs the knot alone, not repos.";
          };

          SNOT_USER_MAP = mkOption {
            type = types.str;
            example = "did:plc:abc123=isabel,did:plc:def456=alice";
            description = "Comma-separated did=user pairs mapping repo-owner DIDs to Forgejo users.";
          };

          SNOT_DB_DSN = mkOption {
            type = types.str;
            default = "postgres://snot@/forgejo?host=/run/postgresql";
            description = ''
              Postgres DSN for the Forgejo database. The default uses
              unix-socket peer auth; create a `snot` role with
              SELECT grants on `"user"`, `repository`, `language_stat`, and
              `public_key`.
            '';
          };

          SNOT_REPO_ROOT = mkOption {
            type = types.str;
            default = "/var/lib/forgejo/repositories";
            description = "Forgejo's repository storage directory.";
          };

          SNOT_PUSH_REMOTE = mkOption {
            type = types.nullOr types.str;
            default = null;
            example = "git@git.example.com";
            description = "SSH remote base suggested when an HTTP push is rejected.";
          };

          SNOT_STATE_DIR = mkOption {
            type = types.str;
            default = "/var/lib/snot";
            description = "Directory holding the signing key and rkey map.";
          };

          SNOT_PLC_URL = mkOption {
            type = types.str;
            default = "https://plc.directory";
            description = "URL of the DID PLC directory.";
          };
        };
      };
    };

    forgejoGroup = mkOption {
      type = types.str;
      default = "forgejo";
      description = "Group with read access to the repository root.";
    };

    environmentFiles = mkOption {
      type = types.listOf types.path;
      default = [ ];
      description = ''
        Files to load environment variables from. Loaded variables override
        values set in {option}`settings`; use them for secrets such as a
        `SNOT_DB_DSN` containing a password.
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
        ExecStart = "${getExe cfg.package} serve";
        Environment = lib.mapAttrsToList (k: v: "${k}=${v}") (
          lib.filterAttrs (_: v: v != null) cfg.settings
        );
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
        ReadOnlyPaths = [ cfg.settings.SNOT_REPO_ROOT ];
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
