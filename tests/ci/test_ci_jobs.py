"""校验跨组件影响范围、实际 Git 差异和汇总门禁。"""

import json
import os
import subprocess
import sys
import unittest
from pathlib import Path
from tempfile import TemporaryDirectory
from typing import Any

from ci_jobs import JOBS, expected_results, verify


class CIJobsTest(unittest.TestCase):
    def test_component_and_shared_paths(self) -> None:
        cases = {
            ("docs/sdk/go/gateway.md",): set(),
            ("sdk/go/gateway/proxy.go", "docs/sdk/go/gateway.md"): {"go"},
            ("sdk/frontend/package-lock.json",): {"frontend"},
            ("sdk/go/objectstorage/s3store/client.go",): {"go", "integration-storage"},
            ("contracts/logging/sanitization-cases.json",): {
                "go",
                "python-logging",
                "integration-storage",
            },
            ("contracts/storage/v1/limits.json",): {
                "go",
                "python-storage",
                "integration-storage",
            },
            ("docs/examples/storage-access.json",): {"python-storage"},
            ("tests/integration/rustfs-setup.sh",): {
                "integration-storage",
                "python-objectstorage",
            },
            ("tests/docs/python_artifact.py",): {
                "python-logging",
                "python-storage",
                "python-objectstorage",
            },
            ("tests/integration/storage-pipeline.py",): {
                "integration-storage",
                "python-storage",
            },
            ("go.work.sum",): {"go", "integration-storage"},
            ("Makefile",): set(JOBS),
            (".github/workflows/ci.yml",): set(JOBS),
            ("tests/ci/ci_jobs.py",): set(JOBS),
            ("contracts/new/protocol.json",): set(JOBS),
        }
        for paths, required in cases.items():
            with self.subTest(paths=paths):
                plan = expected_results(paths)
                self.assertEqual(
                    {job for job in plan if plan[job] == "success"},
                    required | {"shell"},
                )

    def test_verify_requires_expected_results(self) -> None:
        expected = expected_results(["sdk/go/gateway/proxy.go"])
        needs: dict[str, Any] = {
            job: {"result": status} for job, status in expected.items()
        }
        needs["changes"] = {
            "result": "success",
            "outputs": {"expected": json.dumps(expected)},
        }
        verify(needs)
        for job, status in (
            ("go", "failure"),
            ("go", "cancelled"),
            ("go", "skipped"),
            ("frontend", "failure"),
            ("changes", "failure"),
        ):
            with self.subTest(job=job, status=status), self.assertRaises(ValueError):
                verify({**needs, job: {"result": status}})
        with self.assertRaises(ValueError):
            verify({job: value for job, value in needs.items() if job != "go"})
        with self.assertRaises(ValueError):
            verify(
                {
                    **needs,
                    "changes": {"result": "success", "outputs": {"expected": "{}"}},
                }
            )

    def test_push_and_pr_diff_across_renames(self) -> None:
        with TemporaryDirectory() as tmp:
            repo = Path(tmp)

            def git(*args: str) -> str:
                return subprocess.check_output(
                    [
                        "git",
                        "-c",
                        "user.name=CI",
                        "-c",
                        "user.email=ci@example.test",
                        "-c",
                        "core.hooksPath=/dev/null",
                        "-c",
                        "commit.gpgSign=false",
                        *args,
                    ],
                    cwd=repo,
                    text=True,
                ).strip()

            def select(event: str, base: str, head: str) -> set[str]:
                output = repo / ".git" / "output"
                output.write_text("", encoding="utf-8")
                subprocess.run(
                    [
                        sys.executable,
                        str(Path(__file__).resolve().with_name("ci_jobs.py")),
                        "select",
                    ],
                    cwd=repo,
                    check=True,
                    capture_output=True,
                    env={
                        **os.environ,
                        "GITHUB_EVENT_NAME": event,
                        "BASE_SHA": base,
                        "HEAD_SHA": head,
                        "GITHUB_OUTPUT": str(output),
                    },
                )
                plan = json.loads(output.read_text().removeprefix("expected="))
                return {job for job, result in plan.items() if result == "success"}

            git("init", "--quiet", "-b", "main")
            original = repo / "sdk/go/gateway/old\nfile"
            original.parent.mkdir(parents=True)
            original.write_text("payload")
            git("add", ".")
            git("commit", "-qm", "base")
            base = git("rev-parse", "HEAD")
            git("switch", "-qc", "feature")
            moved = repo / "sdk/frontend/new file"
            moved.parent.mkdir(parents=True)
            original.rename(moved)
            git("add", "-A")
            git("commit", "-qm", "move")
            (repo / "README.md").touch()
            git("add", ".")
            git("commit", "-qm", "documentation")
            head = git("rev-parse", "HEAD")
            git("switch", "-q", "main")
            target = repo / "sdk/python/logging/logger.py"
            target.parent.mkdir(parents=True)
            target.touch()
            git("add", ".")
            git("commit", "-qm", "target branch only")

            affected = {"go", "frontend", "shell"}
            self.assertEqual(select("push", base, head), affected)
            self.assertEqual(
                select("pull_request", git("rev-parse", "HEAD"), head), affected
            )
            self.assertEqual(select("push", "0" * 40, head), set(JOBS))
            self.assertEqual(select("push", "f" * 40, head), set(JOBS))
