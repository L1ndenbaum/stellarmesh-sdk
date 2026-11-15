"""根据 Git 差异选择 CI 作业，并核对 Actions 返回的最终状态。"""

import json
import os
import subprocess
import sys
from collections.abc import Iterable
from fnmatch import fnmatchcase
from pathlib import Path
from typing import Any

JOBS = (
    "go",
    "frontend",
    "python-logging",
    "python-storage",
    "python-objectstorage",
    "shell",
    "integration-storage",
)

# 首个匹配项生效；fnmatch 的 * 可以跨目录。新组件、未知契约与全局配置默认全跑。
RULES: dict[str, tuple[str, ...]] = {
    "contracts/README.md": (),
    "contracts/logging/*": ("go", "python-logging", "integration-storage"),
    "contracts/storage/*": ("go", "python-storage", "integration-storage"),
    "contracts/*": JOBS,
    "docs/examples/storage-access.json": ("python-storage",),
    "sdk/frontend/README.md": ("frontend",),
    "sdk/python/logging/README.md": ("python-logging",),
    "sdk/python/storage/README.md": ("python-storage",),
    "sdk/python/objectstorage/README.md": ("python-objectstorage",),
    "*.md": (),
    ".dockerignore": ("integration-storage",),
    "services/storage/Dockerfile": ("integration-storage",),
    "services/storage/*": ("go", "integration-storage"),
    "go.work*": ("go", "integration-storage"),
    # 即使是嵌套模块，依赖版本变动也可能改变工作区中 Storage 集成的依赖解析。
    "sdk/go/*/go.mod": ("go", "integration-storage"),
    "sdk/go/*/go.sum": ("go", "integration-storage"),
    "sdk/go/gateway/*": ("go",),
    "sdk/go/mq/kafka/*": ("go",),
    "sdk/go/*": ("go", "integration-storage"),
    "sdk/frontend/*": ("frontend",),
    "sdk/python/logging/*": ("python-logging",),
    "sdk/python/storage/*": ("python-storage", "integration-storage"),
    "sdk/python/objectstorage/*": ("python-objectstorage",),
    "tests/docs/python_artifact.py": (
        "python-logging",
        "python-storage",
        "python-objectstorage",
    ),
    "tests/docs/go_doc.py": ("go", "python-storage"),
    "tests/docs/*.py": ("python-storage",),
    "tests/go-module-consumer.sh": ("go",),
    "tests/fixtures/go-consumers/*": ("go",),
    "tests/package-consumer/python-objectstorage/*": ("python-objectstorage",),
    "tests/integration/gateway-session-redis.sh": ("go",),
    "tests/integration/storage-pipeline.py": ("python-storage", "integration-storage"),
    "tests/integration/storage-rustfs.sh": ("integration-storage",),
    "tests/integration/objectstorage-rustfs.sh": ("python-objectstorage",),
    "tests/integration/rustfs-setup.sh": (
        "python-objectstorage",
        "integration-storage",
    ),
    ".github/workflows/prepare-frontend.yml": ("frontend",),
    ".github/workflows/release-go.yml": ("go",),
    ".github/workflows/release-python.yml": (
        "python-logging",
        "python-storage",
        "python-objectstorage",
    ),
}


def expected_results(paths: Iterable[str] | None) -> dict[str, str]:
    # 任何文件都可能是文档链接或关联示例的目标，轻量文档和差异检查始终运行。
    selected = {"shell"}
    if paths is None:
        selected.update(JOBS)
    else:
        for path in paths:
            jobs = next(
                (jobs for pattern, jobs in RULES.items() if fnmatchcase(path, pattern)),
                JOBS,
            )
            selected.update(jobs)
    return {job: "success" if job in selected else "skipped" for job in JOBS}


def changed_paths(event: str, base: str, head: str) -> list[str] | None:
    if event not in {"push", "pull_request"}:
        raise ValueError(f"不支持的 CI 事件 {event}")
    if not base or not head or "0" * 40 in (base, head):
        print("缺少比较基线，本次执行全部作业")
        return None
    revision = f"{base}...{head}" if event == "pull_request" else f"{base}..{head}"
    # 禁用重命名折叠，跨组件移动文件时两侧均计入；NUL 分隔保留合法文件名。
    diff = subprocess.run(
        ["git", "diff", "--name-only", "--no-renames", "-z", revision, "--"],
        stdout=subprocess.PIPE,
        check=False,
    )
    if diff.returncode:
        print("无法取得完整变更路径，本次执行全部作业")
        return None
    return [os.fsdecode(path) for path in diff.stdout.split(b"\0") if path]


def verify(needs: dict[str, Any]) -> None:
    changes = needs["changes"]
    if changes["result"] != "success":
        raise ValueError(f"变更识别作业未成功：{changes['result']}")
    expected = json.loads(changes["outputs"]["expected"])
    if (
        set(expected) != set(JOBS)
        or not all(status in {"success", "skipped"} for status in expected.values())
        or expected["shell"] != "success"
    ):
        raise ValueError(f"CI 作业计划无效：{expected}")
    actual = {name: job["result"] for name, job in needs.items() if name != "changes"}
    if actual != expected:
        raise ValueError(f"作业结果与计划不符：预期 {expected}，实际 {actual}")
    print(f"持续验证通过：{actual}")


if __name__ == "__main__":
    if sys.argv[1:] == ["select"]:
        paths = changed_paths(
            os.environ["GITHUB_EVENT_NAME"],
            os.environ["BASE_SHA"],
            os.environ["HEAD_SHA"],
        )
        plan = json.dumps(expected_results(paths), sort_keys=True)
        print(f"作业计划：{plan}")
        with Path(os.environ["GITHUB_OUTPUT"]).open("a", encoding="utf-8") as output:
            print(f"expected={plan}", file=output)
    elif sys.argv[1:] == ["verify"]:
        verify(json.loads(os.environ["CI_NEEDS"]))
    else:
        sys.exit("用法：ci_jobs.py select | verify")
