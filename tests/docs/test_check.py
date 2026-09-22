"""用稳定的文档输入验证离线检查器，不耦合仓库文件数量。"""

import unittest
from pathlib import Path
from tempfile import TemporaryDirectory

from check import anchors, check_file, example_source


class DocsCheckTest(unittest.TestCase):
    def test_headings_and_fences(self) -> None:
        text = (
            '# `配置` API\n## 重复\n## 重复\n```md\n# 隐藏\n```\n<a id="旧入口"></a>\n'
        )
        self.assertEqual(anchors(text), {"配置-api", "重复", "重复-1", "旧入口"})

    def test_local_links_and_anchors(self) -> None:
        with TemporaryDirectory() as folder:
            root = Path(folder)
            page = root / "README.md"
            (root / "中文.md").write_text("# 说明\n", encoding="utf-8")
            page.write_text(
                "[有效](%E4%B8%AD%E6%96%87.md#说明)\n[缺失](missing.md)\n"
                "[坏锚点](中文.md#无)\n`[非链接](nothing.md)`\n"
                "[发布链接](https://github.com/L1ndenbaum/stellarmesh-sdk/blob/dev/中文.md#说明)\n"
                "```md\n[非链接](nothing.md)\n```\n[网站](https://example.invalid)\n",
                encoding="utf-8",
            )
            errors = check_file(root, page)
            self.assertEqual(len(errors), 2)
            self.assertIn("本地目标不存在", errors[0])
            self.assertIn("锚点不存在", errors[1])

    def test_examples_and_drift(self) -> None:
        with TemporaryDirectory() as folder:
            root = Path(folder)
            (root / "sample.py").write_text(
                "# docs:start sample\nprint('示例')\n# docs:end sample\n",
                encoding="utf-8",
            )
            self.assertEqual(
                example_source(root, "sample.py#sample"), "print('示例')\n"
            )
            page = root / "README.md"
            text = "<!-- example: sample.py#sample -->\n```python\nprint('示例')\n```\n<!-- /example -->\n"
            page.write_text(text, encoding="utf-8")
            self.assertEqual(check_file(root, page), [])
            page.write_text(
                text.replace("print('示例')", "print('漂移')"), encoding="utf-8"
            )
            self.assertIn("源码不一致", check_file(root, page)[0])

    def test_boundaries_and_malformed_markers(self) -> None:
        with TemporaryDirectory() as folder:
            root = Path(folder)
            page = root / "README.md"
            page.write_text(
                "[越界](../outside.md)\n<!-- example: sample.py -->\n没有代码块\n",
                encoding="utf-8",
            )
            errors = check_file(root, page)
            self.assertEqual(len(errors), 3)
            self.assertIn("越过仓库边界", errors[0])


if __name__ == "__main__":
    unittest.main()
