"""隔离安装并检查指定 wheel；显式制品目录模式不会重新构建。"""

import subprocess
import sys
import tempfile
from pathlib import Path


def run(*args: str) -> None:
    subprocess.run(args, check=True)


def check(artifacts: Path, workspace: Path) -> None:
    wheels = list(artifacts.glob("*.whl"))
    if len(wheels) != 1:
        raise ValueError("必须提供唯一 wheel")
    environment = workspace / "consumer"
    run("uv", "venv", "--python", sys.executable, str(environment))
    python = str(environment / "bin/python")
    run(
        "uv",
        "pip",
        "install",
        "--python",
        python,
        str(wheels[0]),
        "mypy>=1.10,<2",
    )
    sample = Path(__file__).with_name("sample.py")
    run(python, "-m", "mypy", "--strict", "--no-incremental", str(sample))
    run(python, str(sample))


def main() -> None:
    with tempfile.TemporaryDirectory(prefix="objectstorage-consumer-") as directory:
        workspace = Path(directory)
        if sys.argv[1] == "--build":
            artifacts = workspace / "dist"
            run(
                "uv",
                "build",
                "--project",
                "sdk/python/objectstorage",
                "--out-dir",
                str(artifacts),
            )
        else:
            artifacts = Path(sys.argv[1]).resolve()
        check(artifacts, workspace)


if __name__ == "__main__":
    main()
