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
    {
      self,
      nixpkgs,
      flake-utils,
      flake-checks,
      ...
    }:
    let
      homewizard-p1-exporterVersion = if (self ? shortRev) then self.shortRev else "dev";
      vendorHash = "sha256-E3kwp3exxXo/OJJ1YIxQDi5gB4WDFCRARu+tIfoK3Kg=";
    in
    {
      overlays.default =
        _: prev:
        let
          pkgs = nixpkgs.legacyPackages.${prev.stdenv.hostPlatform.system};
          # buildGoLatestModule, not buildGo127Module: this tracks whatever the
          # newest Go in nixpkgs is, so the next bump is a nixpkgs update only.
          # Bare buildGoModule still resolves to the older default.
          buildGo = pkgs.buildGoLatestModule;
        in
        {
          homewizard-p1-exporter = buildGo {
            pname = "homewizard-p1-exporter";
            version = homewizard-p1-exporterVersion;
            src = pkgs.nix-gitignore.gitignoreSource [ ] ./.;

            subPackages = [ "cmd/homewizard-p1-exporter" ];

            inherit vendorHash;
          };

          # Rebuild the Go dev tools against the latest Go so everything agrees
          # on one version. golangci-lint and gopls already track it upstream.
          gofumpt = prev.gofumpt.override { buildGoModule = buildGo; };
          # goimports ships wrapped with a `go` on PATH. That `go` must be at
          # least the go.mod directive, or GOTOOLCHAIN=auto tries to fetch a
          # toolchain from inside the network-less treefmt sandbox.
          gotools = prev.gotools.override {
            buildGoModule = buildGo;
            go = pkgs.go_latest;
          };
        };
    }
    # eachDefaultSystem still lists x86_64-darwin, which nixpkgs 26.11 dropped:
    # evaluating any output for it throws.
    // flake-utils.lib.eachSystem [ "x86_64-linux" "aarch64-linux" "aarch64-darwin" ] (
      system:
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
          goPkg = pkgs.go_latest;
        };
        buildDeps = with pkgs; [
          git
          go_latest
        ];
        devDeps =
          with pkgs;
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
        apps =
          let
            # mkApp drops meta, and `nix flake check` warns on apps without it.
            app = flake-utils.lib.mkApp { drv = pkgs.homewizard-p1-exporter; } // {
              meta.description = "Run the Homewizard P1 Prometheus exporter";
            };
          in
          {
            homewizard-p1-exporter = app;
            default = app;
          };
      }
    )
    // {
      nixosModules.default =
        {
          pkgs,
          lib,
          config,
          ...
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
