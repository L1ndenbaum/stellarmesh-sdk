"""可执行的标准库日志接入，输出流由调用方管理。"""

import logging
from typing import TextIO

from stellarmesh_logging import JSONFormatter


def write_example(stream: TextIO) -> None:
    """将一条安全 JSON 写入 stream，不修改全局默认 Logger。"""
    logger = logging.Logger("example", level=logging.INFO)
    handler = logging.StreamHandler(stream)
    try:
        handler.setFormatter(JSONFormatter(static_fields={"service": "example-api"}))
        logger.addHandler(handler)
        logger.info("请求完成", extra={"password": "example-secret", "duration_ms": 12})
    finally:
        logger.removeHandler(handler)
        handler.close()
    # StreamHandler.close 不关闭调用方的 stream；文件或 StringIO 由外部 with 关闭。
