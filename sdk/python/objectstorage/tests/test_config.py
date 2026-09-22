from typing import Any

import pytest

from stellarmesh_objectstorage import ClientConfig, InvalidRequestError


@pytest.mark.parametrize(
    "values",
    [
        {"bucket": ""},
        {"region": " "},
        {"prefix": "/prefix"},
        {"endpoint": "https://user:password@objects.test"},
        {"endpoint": "https://objects.test/path"},
        {"endpoint": "https://objects.test?query=secret"},
        {"presign_endpoint": "https://public.test"},
        {"use_path_style": 1},
        {"max_attempts": True},
        {"max_attempts": 0},
        {"connect_timeout": float("nan")},
        {"read_timeout": 0},
        {"default_presign_ttl": 59},
        {"default_presign_ttl": 3601},
    ],
)
def test_invalid_configuration(values: dict[str, Any]) -> None:
    with pytest.raises(InvalidRequestError):
        ClientConfig(**{"bucket": "documents", "region": "us-east-1", **values})


def test_binding_keeps_object_key_content() -> None:
    config = ClientConfig(bucket="documents", region="us-east-1", prefix="project///")
    assert config.physical_key("资料/a b+%.txt") == "project/资料/a b+%.txt"
    assert config.ttl(None) == 900
    for key in ("", "/escape", " padded", "a\n", "x" * 1024):
        with pytest.raises(InvalidRequestError):
            config.physical_key(key)
