"""Sync assets and copy the Nix MaaFramework runtime into ./install/."""

import argparse
import shutil
import stat
import tempfile
from pathlib import Path


def sync_assets(root: Path, install: Path) -> None:
    # These are the only project-owned paths that this command manages.
    # In particular, do not infer install paths from arbitrary working-tree
    # directories: install/config and other runtime state are out of scope.
    asset_directories = (
        "data",
        "locales",
        "resource",
        "resource_adb",
        "resource_cloud_adb",
        "resource_linux",
        "resource_macos",
        "resource_playcover",
        "tasks",
    )
    links = [
        (root / "assets" / name, install / name)
        for name in asset_directories
        if (root / "assets" / name).is_dir()
    ]

    copies = [(root / "assets/interface.json", install / "interface.json")]
    copies.extend((root / name, install / name) for name in ("README.md", "LICENSE"))

    # Validate before replacing anything; preserve existing real directories.
    for _, destination in links + copies:
        if destination.is_dir() and not destination.is_symlink():
            raise SystemExit(f"Move the existing directory aside first: {destination}")
    for source, destination in links + copies:
        destination.parent.mkdir(parents=True, exist_ok=True)
        destination.unlink(missing_ok=True)
        if source.is_dir():
            destination.symlink_to(source, target_is_directory=True)
        else:
            shutil.copy2(source, destination)


def copy_framework(framework: Path, install: Path) -> None:
    # Dereference SDK links so library-relative lookup stays in install/maafw.
    maafw = install / "maafw"
    with tempfile.TemporaryDirectory(prefix=".maafw-", dir=install) as temporary:
        staging = Path(temporary) / "runtime"
        previous = Path(temporary) / "previous"
        shutil.copytree(framework / "lib", staging, symlinks=False)
        staging.chmod(staging.stat().st_mode | stat.S_IWUSR)
        shutil.copytree(
            framework / "share/MaaAgentBinary",
            staging / "MaaAgentBinary",
            symlinks=False,
        )

        # Nix store permissions are read-only; make the local copy maintainable.
        for item in [staging, *staging.rglob("*")]:
            item.chmod(item.stat().st_mode | stat.S_IWUSR)

        if maafw.exists() or maafw.is_symlink():
            maafw.rename(previous)
        try:
            staging.rename(maafw)
        except OSError:
            if previous.exists() or previous.is_symlink():
                previous.rename(maafw)
            raise


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("framework", type=Path, help="MaaFramework Nix package path")
    args = parser.parse_args()

    root = Path.cwd()
    install = root / "install"
    if install.is_symlink():
        parser.error("install/ must be a local directory")

    sync_assets(root, install)
    copy_framework(args.framework, install)
    print("Resources ready; MaaFramework copied to install/maafw")


if __name__ == "__main__":
    main()
