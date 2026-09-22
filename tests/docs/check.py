"""离线校验受版本管理的 Markdown 链接、锚点和源码片段，不改写文档。"""

from __future__ import annotations

import html
import re
import subprocess
import sys
from pathlib import Path
from urllib.parse import unquote, urlsplit

ROOT = Path(__file__).resolve().parents[2]
_LINK = re.compile(r"!?\[[^\]\n]*\]\((?:<([^>]+)>|([^\s)]+))(?:\s+\"[^\"]*\")?\)")
_REFERENCE = re.compile(r"(?m)^\s*\[[^\]]+\]:\s*<?([^\s>]+)>?")
_MARKER = re.compile(r"(?m)^<!-- example: (\S+) -->[ \t]*$")


def visible_markdown(text: str) -> str:
    """用空格遮盖围栏块，保留偏移和行号；围栏中的示例标记不参与检查。"""
    fence = ""
    output: list[str] = []
    for line in text.splitlines(keepends=True):
        match = re.match(r"^\s{0,3}(`{3,}|~{3,})", line)
        hidden = bool(fence)
        if match:
            current = match[1]
            if not fence:
                fence = current
                hidden = True
            elif current[0] == fence[0] and len(current) >= len(fence):
                if not line[match.end() :].strip():
                    fence = ""
        output.append(re.sub(r"[^\n]", " ", line) if hidden else line)
    return "".join(output)


def anchors(text: str) -> set[str]:
    """覆盖仓库采用的 ATX 标题、重复标题和显式 HTML id。"""
    visible = visible_markdown(text)
    result = set(re.findall(r'\bid=["\']([^"\']+)["\']', visible))
    generated: set[str] = set()
    for heading in re.findall(r"(?m)^ {0,3}#{1,6}\s+(.+?)\s*#*\s*$", visible):
        heading = re.sub(r"\[([^]]+)\]\([^)]*\)", r"\1", heading)
        heading = re.sub(r"<[^>]+>", "", heading)
        slug = re.sub(r"[^\w\- ]", "", html.unescape(heading).lower()).replace(" ", "-")
        candidate, index = slug, 0
        while candidate in generated:
            index += 1
            candidate = f"{slug}-{index}"
        generated.add(candidate)
        result.add(candidate)
    return result


def local_target(root: Path, base: Path, value: str) -> Path:
    """链接可从文档目录或仓库根目录解析，拒绝越过仓库边界。"""
    target = (
        root / value.lstrip("/") if value.startswith("/") else base / value
    ).resolve()
    if not target.is_relative_to(root.resolve()):
        raise ValueError("路径越过仓库边界")
    return target


def example_source(root: Path, spec: str) -> str:
    """提取全文或唯一命名片段；片段内部空白保持原样。"""
    name, _, region = spec.partition("#")
    source = local_target(root, root, name).read_text(encoding="utf-8")
    if not region:
        return source
    start = re.compile(rf"^\s*(?://|#) docs:start {re.escape(region)}\s*$")
    end = re.compile(rf"^\s*(?://|#) docs:end {re.escape(region)}\s*$")
    lines = source.splitlines(keepends=True)
    starts = [i for i, line in enumerate(lines) if start.match(line)]
    ends = [i for i, line in enumerate(lines) if end.match(line)]
    if len(starts) != 1 or len(ends) != 1 or starts[0] >= ends[0]:
        raise ValueError("源码片段标记缺失、重复或顺序错误")
    return "".join(lines[starts[0] + 1 : ends[0]])


def check_file(root: Path, path: Path) -> list[str]:
    """返回带文件和行号的诊断，既检查链接也检查关联代码块。"""
    text = path.read_text(encoding="utf-8")
    visible = visible_markdown(text)
    errors: list[str] = []

    def report(offset: int, message: str) -> None:
        line = text.count("\n", 0, offset) + 1
        errors.append(f"{path.relative_to(root)}:{line}: {message}")

    # 行内代码中的括号不是链接；引用式链接检查定义中的目的地。
    links_text = re.sub(r"`+[^`\n]*`+", lambda m: " " * len(m[0]), visible)
    links = [(m.start(), m[1] or m[2]) for m in _LINK.finditer(links_text)]
    links.extend((m.start(), m[1]) for m in _REFERENCE.finditer(links_text))
    for offset, target in links:
        parts = urlsplit(html.unescape(target))
        # 发布平台 README 使用绝对仓库链接，仍可离线验证其目标。
        repository_prefix = "/L1ndenbaum/stellarmesh-sdk/blob/dev/"
        if parts.netloc == "github.com" and parts.path.startswith(repository_prefix):
            parts = parts._replace(
                scheme="", netloc="", path="/" + parts.path[len(repository_prefix) :]
            )
        if parts.scheme or parts.netloc:
            continue
        try:
            dest = (
                local_target(root, path.parent, unquote(parts.path))
                if parts.path
                else path
            )
            if not dest.exists():
                raise ValueError("本地目标不存在")
            fragment = unquote(parts.fragment)
            if fragment and dest.suffix.lower() == ".md":
                if fragment not in anchors(dest.read_text(encoding="utf-8")):
                    raise ValueError("Markdown 锚点不存在")
            elif fragment:
                match = re.fullmatch(r"L([1-9]\d*)(?:-L([1-9]\d*))?", fragment)
                if not match or not dest.is_file():
                    raise ValueError("非 Markdown 目标仅支持源码行号锚点")
                count = len(dest.read_text(encoding="utf-8").splitlines())
                first, last = int(match[1]), int(match[2] or match[1])
                if not 1 <= first <= last <= count:
                    raise ValueError("源码行号越界")
        except (OSError, ValueError) as error:
            report(offset, f"{target}: {error}")

    markers = list(_MARKER.finditer(visible))
    if len(markers) != len(re.findall(r"(?m)^<!-- /example -->[ \t]*$", visible)):
        report(0, "示例开始与结束标记数量不一致")
    for marker in markers:
        # 标记后的内容必须恰好是一个围栏代码块，避免静默忽略错配块。
        block = re.match(
            r"\s*(`{3,}|~{3,})[^\n]*\n(.*?)\n\1\s*\n<!-- /example -->",
            text[marker.end() :],
            re.DOTALL,
        )
        if not block:
            report(marker.start(), "示例标记后缺少完整代码块或结束标记")
            continue
        try:
            source = example_source(root, marker[1])
            if block[2].rstrip("\n") != source.rstrip("\n"):
                raise ValueError("文档代码块与源码不一致，请手工同步")
        except (OSError, ValueError) as error:
            report(marker.start(), f"{marker[1]}: {error}")
    return errors


def main() -> int:
    files = (
        subprocess.check_output(
            [
                "git",
                "ls-files",
                "-z",
                "--cached",
                "--others",
                "--exclude-standard",
                "--",
                "*.md",
            ],
            cwd=ROOT,
        )
        .decode()
        .split("\0")
    )
    paths = sorted({ROOT / name for name in files if name and (ROOT / name).is_file()})
    errors = [error for path in paths for error in check_file(ROOT, path)]
    for error in errors:
        print(error, file=sys.stderr)
    if errors:
        return 1
    print(f"文档检查通过：{len(paths)} 个 Markdown，本地链接、锚点与关联示例一致")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
