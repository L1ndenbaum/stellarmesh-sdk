"""完整预签名请求描述，不包含长期认证凭据。"""

from collections.abc import Mapping
from dataclasses import dataclass
from datetime import datetime


@dataclass(frozen=True, repr=False)
class PresignedRequest:
    """临时访问能力；URL 不可改写，method 与 headers 必须原样执行。

    签名可能因临时凭据先到期而提前失效。不要在日志中输出 URL。
    """

    url: str
    method: str
    headers: Mapping[str, str]
    expires_at: datetime
