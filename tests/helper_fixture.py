"""Activate Go helpers and provide filesystem/lock fixtures for isolated tests."""

from contextlib import contextmanager
import os
from pathlib import Path
import shutil
import stat
import tomllib


ROOT = Path(__file__).resolve().parents[1]
GO_VERSION = tomllib.loads((ROOT / "dotfiles/common/mise/config.toml").read_text())["tools"]["go"]


def prepare_go_config(project):
    """Write only the public version needed by build/version-reader fixtures."""
    config = project / "dotfiles/common/mise/config.toml"
    config.parent.mkdir(parents=True, exist_ok=True)
    config.write_text(f'[tools]\ngo = "{GO_VERSION}"\n')
    return config


def prepare_helper(project):
    current = ROOT / ".system-sync/helper/current"
    if not current.is_file():
        raise RuntimeError("Go helper 未构建，请先运行 mise run helper-build 或 mise run check")
    version, checksum = current.read_text().split()
    source = current.parent / version / checksum
    target = project / ".system-sync/helper" / version / checksum
    shutil.copytree(source, target)
    shutil.copy2(current, target.parent.parent / "current")
    (project / "scripts").mkdir(parents=True, exist_ok=True)
    shutil.copy2(ROOT / "scripts/task-output.sh", project / "scripts/task-output.sh")
    scripts = project / "helper/scripts"
    scripts.mkdir(parents=True)
    for name in ("common.sh", "go-version.sh", "install.sh", "run.sh"):
        shutil.copy2(ROOT / "helper/scripts" / name, scripts / name)
    shutil.copy2(ROOT / "helper/VERSION", project / "helper/VERSION")
    return target / "system-sync-helper"


def snapshot(path):
    """Compare test files without following links or depending on timestamps."""
    path = Path(path)
    try:
        mode = path.lstat().st_mode
    except FileNotFoundError:
        return ("missing",)
    permissions = stat.S_IMODE(mode)
    if stat.S_ISLNK(mode):
        return ("link", permissions, os.readlink(path))
    if stat.S_ISDIR(mode):
        return ("directory", permissions, tuple((child.name, snapshot(child)) for child in sorted(path.iterdir())))
    if stat.S_ISREG(mode):
        return ("file", permissions, path.read_bytes())
    raise AssertionError(f"unsupported test fixture: {path}")


@contextmanager
def operation_lock(root):
    """Hold the POSIX lock for contention tests; production guards live in Go."""
    import fcntl
    state = Path(root) / ".system-sync"
    state.mkdir(parents=True, exist_ok=True)
    with (state / "operation.lock").open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        try:
            yield lock.fileno()
        finally:
            fcntl.flock(lock, fcntl.LOCK_UN)
