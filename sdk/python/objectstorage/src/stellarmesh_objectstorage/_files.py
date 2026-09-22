"""文件传输的原子提交与取消隔离，不改变网络取消语义。"""

import asyncio
import tempfile
from collections.abc import Callable, Iterator
from contextlib import contextmanager
from pathlib import Path
from typing import BinaryIO, TypeVar, cast

T = TypeVar("T")


@contextmanager
def atomic_destination(destination: str | Path) -> Iterator[tuple[BinaryIO, Path]]:
    path = Path(destination)
    # 同目录 rename 保持原子性；失败和 BaseException（包括取消）都清理暂存。
    with tempfile.NamedTemporaryFile(
        dir=path.parent, prefix=f".{path.name}.", delete=False
    ) as output:
        temporary = Path(output.name)
        try:
            yield cast(BinaryIO, output), temporary
            output.close()
            temporary.replace(path)
        finally:
            temporary.unlink(missing_ok=True)


async def file_io(operation: Callable[[], T]) -> T:
    # 线程中的文件读写不可强制取消，先等其结束再释放文件；不用于网络请求。
    task = asyncio.create_task(asyncio.to_thread(operation))
    canceled: asyncio.CancelledError | None = None
    while not task.done():
        try:
            await asyncio.shield(task)
        except asyncio.CancelledError as error:
            canceled = error
        except Exception:
            break
    if canceled is not None:
        if not task.cancelled():
            task.exception()
        raise canceled
    return task.result()
