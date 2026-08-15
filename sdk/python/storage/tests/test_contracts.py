"""Storage v1 文件契约和 Python 常量一致性测试。"""

from __future__ import annotations

import json
from pathlib import Path

import jsonschema
import pytest
import yaml
from openapi_spec_validator import validate
from pydantic import ValidationError

from stellarmesh_storage.constants import (
    DEFAULT_PRESIGN_TTL_SECONDS,
    MAX_CONTENT_TYPE_BYTES,
    MAX_CONTROL_BODY_BYTES,
    MAX_METADATA_ITEMS,
    MAX_METADATA_UTF8_BYTES,
    MAX_PART_NUMBER,
    MAX_PHYSICAL_KEY_BYTES,
    MAX_PRESIGN_TTL_SECONDS,
    MAX_SINGLE_PUT_BYTES,
    MIN_PART_NUMBER,
    MIN_PRESIGN_TTL_SECONDS,
    MIN_TOKEN_CHARACTERS,
)
from stellarmesh_storage.models import ClientConfig, ObjectRequest, PresignPutRequest

_ROOT = Path(__file__).resolve().parents[4]
_CONTRACT = _ROOT / "contracts" / "storage" / "v1"


def test_limits_match_python_constants() -> None:
    limits = json.loads((_CONTRACT / "limits.json").read_text())
    assert limits == {
        "schema_version": "v1",
        "max_control_body_bytes": MAX_CONTROL_BODY_BYTES,
        "namespace_pattern": "^[a-z][a-z0-9_-]{0,63}$",
        "max_physical_key_bytes": MAX_PHYSICAL_KEY_BYTES,
        "max_metadata_items": MAX_METADATA_ITEMS,
        "max_metadata_utf8_bytes": MAX_METADATA_UTF8_BYTES,
        "max_content_type_bytes": MAX_CONTENT_TYPE_BYTES,
        "min_token_unicode_characters": MIN_TOKEN_CHARACTERS,
        "default_presign_ttl_seconds": DEFAULT_PRESIGN_TTL_SECONDS,
        "min_presign_ttl_seconds": MIN_PRESIGN_TTL_SECONDS,
        "max_presign_ttl_seconds": MAX_PRESIGN_TTL_SECONDS,
        "min_multipart_part_number": MIN_PART_NUMBER,
        "max_multipart_part_number": MAX_PART_NUMBER,
        "max_single_put_bytes": MAX_SINGLE_PUT_BYTES,
    }


def test_access_schema_and_openapi_are_valid() -> None:
    schema = json.loads((_CONTRACT / "access-config.schema.json").read_text())
    valid = json.loads(
        (_CONTRACT / "testdata" / "valid-access-config.json").read_text()
    )
    jsonschema.Draft202012Validator.check_schema(schema)
    jsonschema.validate(valid, schema)
    document = yaml.safe_load((_CONTRACT / "openapi.yaml").read_text())
    validate(document)
    assert set(document["paths"]) == {
        "/health",
        "/health/live",
        "/health/ready",
        "/metrics",
        "/v1/objects/stat",
        "/v1/objects/delete",
        "/v1/presign/get",
        "/v1/presign/put",
        "/v1/multipart/create",
        "/v1/multipart/presign-part",
        "/v1/multipart/complete",
        "/v1/multipart/abort",
    }


def test_models_reject_unknown_and_boundary_values() -> None:
    with pytest.raises(ValidationError):
        ObjectRequest.model_validate(
            {"namespace": "documents", "key": "key", "bucket": "forbidden"}
        )
    with pytest.raises(ValidationError):
        ObjectRequest(namespace="documents", key="/absolute")
    with pytest.raises(ValidationError):
        PresignPutRequest(
            namespace="documents",
            key="key",
            size=MAX_SINGLE_PUT_BYTES + 1,
        )
    with pytest.raises(ValidationError):
        PresignPutRequest(
            namespace="documents",
            key="key",
            size=1,
            metadata={"k": "界" * 700},
        )


def test_client_config_hides_token_from_errors_and_serialization() -> None:
    secret = "short-secret"
    with pytest.raises(ValidationError) as captured:
        ClientConfig(
            base_url="http://storage-service:8090",
            token=secret,
            timeout_seconds=5.0,
        )
    assert secret not in str(captured.value)
    config = ClientConfig(
        base_url="http://storage-service:8090",
        token="storage-contract-token-000000000001",
        timeout_seconds=5.0,
    )
    assert "token" not in config.model_dump()
    assert config.token not in repr(config)
