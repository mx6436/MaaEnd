{
  pkgs,
  lib,
  maaFramework,
}:
let
  command = name: body: pkgs.writeShellScriptBin name ''
    set -euo pipefail
    cd "$(${pkgs.git}/bin/git rev-parse --show-toplevel)"
    ${body}
  '';
in
{
  maaend-sync = command "maaend-sync" ''
    exec ${pkgs.python3}/bin/python3 ${./sync.py} ${maaFramework}
  '';

  maaend-build-go = command "maaend-build-go" ''
    mkdir -p install/agent
    cd agent/go-service
    CGO_ENABLED=0 ${pkgs.go}/bin/go build -mod=readonly -gcflags="all=-N -l" -o ../../install/agent/go-service .
  '';

  maaend-build-cpp = command "maaend-build-cpp" ''
    cores="''${CMAKE_BUILD_PARALLEL_LEVEL:-$(nproc)}"
    printf 'Building cpp-algo with %s cores...\n' "$cores"
    ${pkgs.nix}/bin/nix build --no-link --print-build-logs --option cores "$cores" \
      .#cpp-algo
    output=$(${pkgs.nix}/bin/nix build --no-link --print-out-paths .#cpp-algo)
    mkdir -p install/agent .nix/gc
    ${pkgs.nix}/bin/nix-store --add-root "$PWD/.nix/gc/cpp-algo" --indirect --realise "$output" >/dev/null
    install -m755 "$output/agent/cpp-algo" install/agent/cpp-algo
    echo 'cpp-algo installed to install/agent/cpp-algo'
  '';

  maaend-install-mxu = command "maaend-install-mxu" ''
    cores="''${CMAKE_BUILD_PARALLEL_LEVEL:-$(nproc)}"
    printf 'Building MXU from the locked NUR package with %s cores...\n' "$cores"
    ${pkgs.nix}/bin/nix build --no-link --print-build-logs --option cores "$cores" \
      .#mxu
    output=$(${pkgs.nix}/bin/nix build --no-link --print-out-paths .#mxu)
    ${pkgs.patchelf}/bin/patchelf --print-interpreter "$output/lib/mxu" >/dev/null
    mkdir -p install .nix/gc
    ${pkgs.nix}/bin/nix-store --add-root "$PWD/.nix/gc/mxu" --indirect --realise "$output" >/dev/null

    echo 'Installing MXU binary and wrapGAppsHook3 launcher...'
    install -m755 "$output/lib/mxu" install/.mxu-wrapped
    install -m755 "$output/bin/mxu" install/mxu

    # Remove legacy wrapProgram suffix copies, keeping the installed ELF.
    shopt -s nullglob
    for previous in "$PWD"/install/.mxu-wrapped_*; do
      if [[ "$previous" =~ /\.mxu-wrapped_+$ ]]; then
        printf 'Removing old MXU binary: %s\n' "$previous"
        rm -- "$previous"
      fi
    done

    echo 'MXU ready: install/mxu (wrapGAppsHook3; binary: install/.mxu-wrapped)'
    echo 'Launch with: ./install/mxu'
  '';

  maaend-setup = command "maaend-setup" ''
    maaend-sync
    maaend-build-go
    maaend-build-cpp
    maaend-install-mxu
    echo 'MaaEnd is ready. Launch with: ./install/mxu'
  '';
}
