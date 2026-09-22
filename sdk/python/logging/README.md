# Python Logging

Python 3.11 及以上，无第三方运行依赖；使用标准库 logging，格式与级别由应用选择。

## 安装

示例适用于 `0.5.0`，当前发布状态见[发布矩阵](https://github.com/L1ndenbaum/stellarmesh-sdk/blob/dev/docs/release.md#当前制品矩阵)。

```sh
python -m pip install stellarmesh-logging==0.5.0
```

## 最小完整示例

在应用中调用 `write_example(sys.stdout)`；示例测试使用 StringIO 并断言实际 JSON 输出与脱敏。

<!-- example: sdk/python/logging/tests/example_logging.py -->
```python
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
```
<!-- /example -->

## 关键限制

Formatter 不持有输出流、不发送 HTTP 或启动后台任务。JSON 用于结构化采集，PrettyFormatter 用于本地阅读；清洗不会扫描任意消息正文。

## 深入指南

[接入与迁移](https://github.com/L1ndenbaum/stellarmesh-sdk/blob/dev/docs/sdk/python/README.md)、[架构边界](https://github.com/L1ndenbaum/stellarmesh-sdk/blob/dev/docs/sdk-content.md)、[贡献与验证](https://github.com/L1ndenbaum/stellarmesh-sdk/blob/dev/CONTRIBUTING.md)。源码和注释以主干为准，已发布制品行为以对应 tag 为准。
