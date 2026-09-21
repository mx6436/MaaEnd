{
  pkgs,
  lib,
  nur,
  maaFramework,
  maaUtils,
  sourceRoot,
}:
let
  source = lib.cleanSourceWith {
    src = sourceRoot;
    filter =
      path: type:
      let
        relative = lib.removePrefix "${toString sourceRoot}/" (toString path);
      in
      (relative == "agent" || relative == "agent/cpp-algo" || lib.hasPrefix "agent/cpp-algo/" relative)
      && lib.cleanSourceFilter path type
      && !(lib.elem (baseNameOf path) [
        "build"
        "MaaDeps"
      ])
      && !(lib.hasPrefix "build-" (baseNameOf path));
  };

  sourceWithMaaUtils = pkgs.runCommand "maaend-cpp-algo-source" { } ''
    cp -r ${source} "$out"
    chmod -R u+w "$out"
    rm -rf "$out/agent/cpp-algo/MaaUtils"
    cp -r ${maaUtils} "$out/agent/cpp-algo/MaaUtils"
  '';

  package = pkgs.callPackage "${nur}/pkgs/maaend/cpp-algo.nix" {
    pname = "maaend-local";
    version = "dev";
    src = sourceWithMaaUtils;
    maaUtils = "${sourceWithMaaUtils}/agent/cpp-algo/MaaUtils";
    maa-framework = maaFramework;
    meta = { };
  };
in
package.overrideAttrs {
  # Keep local MaaUtils and development data paths. NUR's dependency
  # substitutions run only in the sandbox, never in the worktree.
  prePatch = "";
  patches = [ ];
  cmakeBuildType = "RelWithDebInfo";
  dontStrip = true;
}
