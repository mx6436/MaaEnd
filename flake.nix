{
  description = "MaaEnd development environment";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    nur-packages = {
      url = "github:mx6436/nur-packages";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    maaUtils = {
      url = "github:MaaXYZ/MaaUtils";
      flake = false;
    };
  };

  outputs =
    {
      nixpkgs,
      nur-packages,
      maaUtils,
      ...
    }:
    let
      systems = [ "x86_64-linux" ];
      perSystem =
        system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
          lib = nixpkgs.lib;
          nur = nur-packages;
          maaFramework = nur.packages.${system}.maa-framework;
          commands = import ./nix/commands.nix {
            inherit pkgs lib maaFramework;
          };
          packages = commands // {
            cpp-algo = import ./nix/cpp-algo.nix {
              inherit
                pkgs
                lib
                nur
                maaFramework
                maaUtils
                ;
              sourceRoot = ./.;
            };

            mxu = import ./nix/mxu.nix {
              inherit pkgs lib nur;
            };
          };
        in
        {
          inherit packages;

          devShell = pkgs.mkShell {
            packages =
              with pkgs;
              [
                android-tools
                go
                gopls
                jq
                nodejs
                pnpm
                python3
                uv
              ]
              ++ builtins.attrValues commands;

            env = {
              UV_PYTHON = "${pkgs.python3}/bin/python3";
              UV_PYTHON_DOWNLOADS = "never";
            };

            shellHook = ''
              echo 'MaaEnd: maaend-setup | maaend-sync | maaend-build-go | maaend-build-cpp | maaend-install-mxu'
            '';
          };
        };
    in
    {
      packages = nixpkgs.lib.genAttrs systems (system: (perSystem system).packages);
      devShells = nixpkgs.lib.genAttrs systems (system: {
        default = (perSystem system).devShell;
      });
    };
}
