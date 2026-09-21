{
  pkgs,
  lib,
  nur,
}:
let
  unwrapped = (nur.packages.${pkgs.stdenv.hostPlatform.system}.mxu-unwrapped).overrideAttrs {
    # install/ is writable: retain upstream's portable data paths and FS scope.
    # Keep automatic updates disabled so they cannot replace this Nix build.
    patches = [ "${nur}/pkgs/mxu-unwrapped/0003-disable-autoupdate.patch" ];
  };
in
pkgs.stdenvNoCC.mkDerivation {
  pname = "maaend-mxu-launcher";
  inherit (unwrapped) version;
  dontUnpack = true;
  dontStrip = true;

  # Match NUR's MaaEnd packaging: GTK environment is collected by the hook,
  # and application-specific additions go through gappsWrapperArgs.
  nativeBuildInputs = [ pkgs.wrapGAppsHook3 ];
  buildInputs = [ pkgs.glib-networking ];

  installPhase = ''
    runHook preInstall
    mkdir -p "$out/bin" "$out/lib"
    cp ${unwrapped}/bin/mxu "$out/lib/mxu"

    # The hook wraps this launcher in the store. The actual ELF must execute
    # from install/ so MXU resolves its resources and writable data locally.
    cat > "$out/bin/mxu" <<'EOF'
    #!${pkgs.runtimeShell}
    set -euo pipefail

    if [[ -x "$PWD/install/.mxu-wrapped" ]]; then
      install_dir="$PWD/install"
    elif [[ -x "$PWD/.mxu-wrapped" ]]; then
      install_dir="$PWD"
    else
      root=$(${pkgs.git}/bin/git rev-parse --show-toplevel 2>/dev/null || true)
      install_dir="${root:+$root/install}"
    fi

    if [[ -z "$install_dir" || ! -x "$install_dir/.mxu-wrapped" ]]; then
      echo 'Unable to locate install/.mxu-wrapped; run this launcher from the MaaEnd project' >&2
      exit 1
    fi

    cd "$install_dir"
    exec "$install_dir/.mxu-wrapped" "$@"
    EOF
    chmod +x "$out/bin/mxu"
    runHook postInstall
  '';

  preFixup = ''
    gappsWrapperArgs+=(
      --prefix LD_LIBRARY_PATH : ${lib.makeLibraryPath [ pkgs.libayatana-appindicator ]}
      --prefix PATH : ${lib.makeBinPath [ pkgs.android-tools ]}
    )
  '';
}
