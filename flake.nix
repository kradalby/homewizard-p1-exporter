{
  description = "homewizard-p1-exporter";

  inputs = {
    nixpkgs.url = "nixpkgs/nixpkgs-unstable";
    utils.url = "github:numtide/flake-utils";
  };

  outputs = {
    self,
    nixpkgs,
    utils,
    ...
  }: let
    homewizard-p1-exporterVersion =
      if (self ? shortRev)
      then self.shortRev
      else "dev";
  in
    {
      overlays.default = _: prev: let
        pkgs = nixpkgs.legacyPackages.${prev.stdenv.hostPlatform.system};
      in {
        homewizard-p1-exporter = pkgs.callPackage ({buildGo126Module}:
          buildGo126Module {
            pname = "homewizard-p1-exporter";
            version = homewizard-p1-exporterVersion;
            src = pkgs.nix-gitignore.gitignoreSource [] ./.;

            subPackages = ["cmd/homewizard-p1-exporter"];

            vendorHash = "sha256-OMFCOokySFCGWXA2udvKroawSLHC4y4SHPM/wUpEhUg=";
          }) {};
      };
    }
    // utils.lib.eachDefaultSystem
    (system: let
      pkgs = import nixpkgs {
        overlays = [self.overlays.default];
        inherit system;
      };
      buildDeps = with pkgs; [
        git
        gnumake
        go_1_26
      ];
      devDeps = with pkgs;
        buildDeps
        ++ [
          golangci-lint
          entr
        ];
    in {
      # `nix develop`
      devShells.default = pkgs.mkShell {
        buildInputs = devDeps;
      };

      # `nix build`
      packages = with pkgs; {
        inherit homewizard-p1-exporter;
        default = homewizard-p1-exporter;
      };

      # `nix run`
      apps = {
        homewizard-p1-exporter = utils.lib.mkApp {
          drv = pkgs.homewizard-p1-exporter;
        };
        default = utils.lib.mkApp {
          drv = pkgs.homewizard-p1-exporter;
        };
      };
    })
    // {
      nixosModules.default = {
        pkgs,
        lib,
        config,
        ...
      }: let
        cfg = config.services.homewizard-p1-exporter;
      in {
        options = with lib; {
          services.homewizard-p1-exporter = {
            enable = mkEnableOption "homewizard-p1-exporter";

            package = mkOption {
              type = types.package;
              description = ''
                homewizard-p1-exporter package to use
              '';
              default = pkgs.homewizard-p1-exporter;
            };

            listenAddr = mkOption {
              type = types.str;
              default = ":9090";
            };
          };
        };
        config = lib.mkIf cfg.enable {
          systemd.services.homewizard-p1-exporter = {
            enable = true;
            script = ''
              export HOMEWIZARD_EXPORTER_LISTEN_ADDR=${cfg.listenAddr}
              ${cfg.package}/bin/homewizard-p1-exporter
            '';
            wantedBy = ["multi-user.target"];
            after = ["network-online.target"];
            wants = ["network-online.target"];
            serviceConfig = {
              DynamicUser = true;
              Restart = "always";
              RestartSec = "15";
            };
            path = [cfg.package];
          };
        };
      };
    };
}
