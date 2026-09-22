"""验证文档示例的真实输出及公开 help 信息。"""

import inspect
import json
from io import StringIO

from example_logging import write_example

from stellarmesh_logging import JSONFormatter, PrettyFormatter


def test_logging_example() -> None:
    with StringIO() as output:
        write_example(output)
        record = json.loads(output.getvalue())
        assert record["password"] == "[REDACTED]"
        assert record["duration_ms"] == 12
        assert record["service"] == "example-api"


def test_public_formatter_help() -> None:
    for formatter in (JSONFormatter, PrettyFormatter):
        assert "max_message_bytes" in (inspect.getdoc(formatter) or "")
        assert "LogRecord" in (inspect.getdoc(formatter.format) or "")
