"""显式分片上传标识与完成参数，业务方负责会话保存和放弃清理。"""

from dataclasses import dataclass


@dataclass(frozen=True)
class MultipartUpload:
    """创建结果；key 为逻辑键，upload_id 原样交回后续操作。"""

    key: str
    upload_id: str


@dataclass(frozen=True)
class CompletedPart:
    """上传成功的分片编号和存储端 ETag；不能用本地摘要替代 ETag。"""

    part_number: int
    etag: str
