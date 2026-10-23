"""Check the test interpreter, then delegate system preflight to the Go helper."""

from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]


def main():
    if sys.version_info < (3, 11):
        print("错误：测试需要 Python 3.11+，请先准备符合要求的 python3", file=sys.stderr)
        return 1
    return subprocess.run([str(ROOT / "helper/scripts/run.sh"), "runtime", "--check"], cwd=ROOT).returncode


if __name__ == "__main__":
    sys.exit(main())
