"""构建或只读检查 Python 制品中的 README 与可供 help 查阅的公共说明。"""

from __future__ import annotations

import importlib
import inspect
import subprocess
import sys
import tarfile
import tempfile
import zipfile
from pathlib import Path


def check(project: Path, artifacts: Path) -> None:
    """在新 Python 进程中从 wheel 导入，不退回源码目录。"""
    wheels = list(artifacts.glob("*.whl"))
    sources = list(artifacts.glob("*.tar.gz"))
    if len(wheels) != 1 or len(sources) != 1:
        raise ValueError("需要唯一 wheel 和 sdist，检查不会重建或替换制品")
    readme = (project / "README.md").read_text(encoding="utf-8").strip()
    with zipfile.ZipFile(wheels[0]) as archive:
        name = next(name for name in archive.namelist() if name.endswith("/METADATA"))
        if readme not in archive.read(name).decode():
            raise ValueError("wheel 元数据未保留组件 README")
    with tarfile.open(sources[0]) as archive:
        name = next(name for name in archive.getnames() if name.endswith("/README.md"))
        stream = archive.extractfile(name)
        if stream is None or stream.read().decode().strip() != readme:
            raise ValueError("sdist README 不一致")
    module_name = "stellarmesh_" + project.name
    sys.path.insert(0, str(wheels[0].resolve()))
    module = importlib.import_module(module_name)
    if not str(module.__file__).startswith(str(wheels[0].resolve())):
        raise ValueError("公共说明检查必须从 wheel 导入")
    names = (
        ("JSONFormatter", "PrettyFormatter")
        if project.name == "logging"
        else ("Client", "AsyncClient", "ClientConfig", "PresignedRequest")
    )
    for name in names:
        value = getattr(module, name)
        if not inspect.getdoc(value):
            raise ValueError(f"{name} 的公共说明未保留")
        if name in {"Client", "AsyncClient", "JSONFormatter", "PrettyFormatter"}:
            for method_name, method in inspect.getmembers(value, inspect.isfunction):
                if (
                    not method_name.startswith("_")
                    and method.__module__.startswith(module_name)
                    and not inspect.getdoc(method)
                ):
                    raise ValueError(f"{name}.{method_name} 缺少公开说明")
    print(f"{module_name} wheel／sdist 的 README 与运行时公共说明检查通过")


def main() -> None:
    if len(sys.argv) == 3 and sys.argv[1] == "--build":
        project = Path(sys.argv[2]).resolve()
        with tempfile.TemporaryDirectory(prefix="stellarmesh-doc-artifact-") as temp:
            subprocess.run(
                [sys.executable, "-m", "build", str(project), "--outdir", temp],
                check=True,
            )
            subprocess.run(
                [
                    sys.executable,
                    "-m",
                    "twine",
                    "check",
                    *map(str, Path(temp).iterdir()),
                ],
                check=True,
            )
            subprocess.run([sys.executable, __file__, str(project), temp], check=True)
    elif len(sys.argv) == 3:
        check(Path(sys.argv[1]).resolve(), Path(sys.argv[2]).resolve())
    else:
        raise SystemExit(
            "用法：python_artifact.py --build 项目目录，或 项目目录 制品目录"
        )


if __name__ == "__main__":
    main()
