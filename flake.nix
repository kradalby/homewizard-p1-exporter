{
  description = "homewizard-p1-exporter";

  inputs = {
    nixpkgs.url = "nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
    flake-checks.url = "github:kradalby/flake-checks";
    flake-checks.inputs.nixpkgs.follows = "nixpkgs";
    flake-checks.inputs.flake-utils.follows = "flake-utils";
  };

  outputs =
    { self
    , nixpkgs
    , flake-utils
    , flake-checks
    , ...
    }:
    let
      homewizard-p1-exporterVersion =
        if (self ? shortRev)
        then self.shortRev
        else "dev";
      vendorHash = "sha256-ptsMn5plnSLvfbbiDMBmshlBNiUHGVKHU3Ex/2vly3s=";
    in
    {
      overlays.default = _: prev:
        let
          pkgs = nixpkgs.legacyPackages.${prev.stdenv.hostPlatform.system};
        in
        {
          homewizard-p1-exporter = pkgs.callPackage
            ({ buildGo126Module }:
              buildGo126Module {
                pname = "homewizard-p1-exporter";
                version = homewizard-p1-exporterVersion;
                src = pkgs.nix-gitignore.gitignoreSource [ ] ./.;

                subPackages = [ "cmd/homewizard-p1-exporter" ];

                inherit vendorHash;
              })
            { };
        };
    }
    // flake-utils.lib.eachDefaultSystem
      (system:
      let
        pkgs = import nixpkgs {
          overlays = [ self.overlays.default ];
          inherit system;
        };
        fc = flake-checks.lib;
        common = {
          inherit pkgs;
          root = ./.;
          pname = "homewizard-p1-exporter";
          version = homewizard-p1-exporterVersion;
          inherit vendorHash;
          goPkg = pkgs.go_1_26;
        };
        buildDeps = with pkgs; [
          git
          go_1_26
        ];
        devDeps = with pkgs;
          buildDeps
          ++ [
            golangci-lint
            gofumpt
            gopls
            prek
            entr
          ];
      in
      {
        # `nix develop`
        devShells.default = pkgs.mkShell {
          buildInputs = devDeps;
        };

        # `nix build`
        packages = {
          inherit (pkgs) homewizard-p1-exporter;
          default = fc.goBuild common;
        };

        formatter = fc.formatter common;

        checks = {
          build = fc.goBuild common;
          gotest = fc.goTest common;
          golangci-lint = fc.goLint common;
          formatting = fc.goFormat common;
        };

        # `nix run`
        apps = {
          homewizard-p1-exporter = flake-utils.lib.mkApp {
            drv = pkgs.homewizard-p1-exporter;
          };
          default = flake-utils.lib.mkApp {
            drv = pkgs.homewizard-p1-exporter;
          };
        };
      })
    // {
      nixosModules.default =
        { pkgs
        , lib
        , config
        , ...
        }:
        let
          cfg = config.services.homewizard-p1-exporter;
        in
        {
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
              wantedBy = [ "multi-user.target" ];
              after = [ "network-online.target" ];
              wants = [ "network-online.target" ];
              serviceConfig = {
                DynamicUser = true;
                Restart = "always";
                RestartSec = "15";
              };
              path = [ cfg.package ];
            };
          };
        };
    };
}
