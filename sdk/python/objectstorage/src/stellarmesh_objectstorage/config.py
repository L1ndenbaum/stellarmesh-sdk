"""单个 Bucket／Prefix 的不可变连接配置。"""

import math
import unicodedata
from dataclasses import dataclass
from urllib.parse import urlsplit

from .errors import InvalidRequestError


def text(value: str, name: str, *, empty: bool = False) -> str:
    if not isinstance(value, str) or (not empty and not value):
        raise InvalidRequestError(f"{name} 必须是非空字符串")
    try:
        value.encode("utf-8")
    except UnicodeError as error:
        raise InvalidRequestError(f"{name} 必须是有效 UTF-8") from error
    if any(unicodedata.category(char) == "Cc" for char in value):
        raise InvalidRequestError(f"{name} 不能包含控制字符")
    return value


@dataclass(frozen=True, kw_only=True)
class ClientConfig:
    """绑定存储位置和传输策略，不持有凭据。

    Args:
        bucket: 已由部署创建的 Bucket。
        region: 显式签名 Region。
        prefix: 可选物理前缀；非空时规范为一个结尾斜杠。
        endpoint: 内部 S3 根地址；None 使用 AWS 标准端点。
        presign_endpoint: 面向签名消费者的根地址，省略沿用 endpoint。
        use_path_style: MinIO 常设为 True，默认 False。
        connect_timeout: 连接超时秒数，默认 5。
        read_timeout: 单次读取超时秒数，默认 5，不是整次传输期限。
        max_attempts: standard 模式总尝试次数，包含首次，默认 3。
        default_presign_ttl: 预签名默认秒数，默认 900，范围 60～3600。
    """

    bucket: str
    region: str
    prefix: str = ""
    endpoint: str | None = None
    presign_endpoint: str | None = None
    use_path_style: bool = False
    connect_timeout: float = 5.0
    read_timeout: float = 5.0
    max_attempts: int = 3
    default_presign_ttl: int = 900

    def __post_init__(self) -> None:
        for name in ("bucket", "region"):
            value = text(getattr(self, name), name)
            if value != value.strip():
                raise InvalidRequestError(f"{name} 不能包含首尾空白")
        prefix = text(self.prefix, "prefix", empty=True)
        if prefix.startswith("/"):
            raise InvalidRequestError("prefix 不能以 / 开头")
        prefix = prefix.rstrip("/")
        object.__setattr__(self, "prefix", prefix + "/" if prefix else "")
        if len(self.prefix.encode()) > 1024:
            raise InvalidRequestError("prefix 超过对象键字节限制")
        for name in ("endpoint", "presign_endpoint"):
            value = getattr(self, name)
            if value is not None:
                self._validate_endpoint(value)
        if self.presign_endpoint is not None and self.endpoint is None:
            raise InvalidRequestError("配置 presign_endpoint 时必须设置 endpoint")
        if type(self.use_path_style) is not bool:
            raise InvalidRequestError("use_path_style 必须是布尔值")
        for timeout in (self.connect_timeout, self.read_timeout):
            if (
                type(timeout) not in (int, float)
                or not math.isfinite(timeout)
                or timeout <= 0
            ):
                raise InvalidRequestError("超时必须是有限正数")
        if type(self.max_attempts) is not int or not 1 <= self.max_attempts <= 10:
            raise InvalidRequestError("max_attempts 必须在 1～10 之间")
        self.ttl(self.default_presign_ttl)

    @staticmethod
    def _validate_endpoint(value: str) -> None:
        text(value, "endpoint")
        try:
            parsed = urlsplit(value)
            _ = parsed.port
        except ValueError as error:
            raise InvalidRequestError("endpoint 非法") from error
        if (
            value != value.strip()
            or parsed.scheme not in {"http", "https"}
            or not parsed.hostname
            or parsed.username is not None
            or parsed.password is not None
            or parsed.path not in {"", "/"}
            or parsed.query
            or parsed.fragment
        ):
            raise InvalidRequestError(
                "endpoint 必须是无凭据、路径或查询参数的 HTTP(S) 根地址"
            )

    def physical_key(self, key: str) -> str:
        """组合 Prefix 与逻辑 key，不裁剪或规范化对象键内容。"""
        text(key, "key")
        if key != key.strip() or key.startswith("/"):
            raise InvalidRequestError("key 不能以 / 开头或包含首尾空白")
        result = self.prefix + key
        if len(result.encode()) > 1024:
            raise InvalidRequestError("物理对象键超过 1024 字节")
        return result

    def ttl(self, value: int | None) -> int:
        """解析预签名秒数；None 使用客户端默认值。"""
        result = self.default_presign_ttl if value is None else value
        if type(result) is not int or not 60 <= result <= 3600:
            raise InvalidRequestError("预签名有效期必须在 60～3600 秒之间")
        return result
