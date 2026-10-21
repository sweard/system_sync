"""Copy the explicitly built helper into disposable test repositories."""

from pathlib import Path
import shutil


ROOT = Path(__file__).resolve().parents[1]


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
    scripts = project / "helper/scripts"
    scripts.mkdir(parents=True)
    for name in ("common.sh", "install.sh", "run.sh"):
        shutil.copy2(ROOT / "helper/scripts" / name, scripts / name)
    shutil.copy2(ROOT / "helper/VERSION", project / "helper/VERSION")
    return target / "system-sync-helper"
