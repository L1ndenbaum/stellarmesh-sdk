from __future__ import annotations

import json
from pathlib import Path

import jsonschema
import pytest
import yaml
from openapi_spec_validator import validate
from openapi_spec_validator.readers import read_from_filename
from pydantic import ValidationError

from stellarmesh_logging import (
    MAX_EVENT_JSON_BYTES,
    MAX_HTTP_BODY_BYTES,
    MAX_KAFKA_KEY_VALUE_BYTES,
    MAX_KAFKA_MESSAGE_BYTES,
    DeadLetter,
    EventKind,
    Level,
    LogEvent,
    OversizeDeadLetter,
    decode_event,
    encode_event,
    should_emit_level,
)

_REPOSITORY_ROOT = Path(__file__).parents[4]


def test_contract_fixtures_round_trip() -> None:
    fixture = (
        _REPOSITORY_ROOT
        / "contracts"
        / "logging"
        / "v2"
        / "testdata"
        / "valid-events.json"
    )
    events = [
        decode_event(json.dumps(payload)) for payload in json.loads(fixture.read_text())
    ]

    assert {event.kind for event in events} == {EventKind.LOG, EventKind.AUDIT}
    assert {event.level for event in events} == set(Level)
    assert json.loads(encode_event(events[0]))["kind"] == "LOG"


def test_event_sanitizes_metadata() -> None:
    event = LogEvent(
        service="backend",
        message="created",
        metadata={"api_token": "secret", "nested": {"safe": "value"}},
    )
    assert event.metadata["api_token"] == "[REDACTED]"
    assert event.metadata["nested"] == {"safe": "value"}


def test_event_rejects_invalid_identifier() -> None:
    with pytest.raises(ValidationError):
        LogEvent(event_id="not-a-uuid", service="backend", message="invalid")


def test_shared_invalid_fixtures_are_rejected() -> None:
    fixture_path = (
        _REPOSITORY_ROOT
        / "contracts"
        / "logging"
        / "v2"
        / "testdata"
        / "invalid-events.json"
    )
    for fixture in json.loads(fixture_path.read_text()):
        with pytest.raises((ValidationError, ValueError)):
            decode_event(json.dumps(fixture["payload"]))


def test_level_filtering() -> None:
    assert should_emit_level("WARN", "INFO")
    assert not should_emit_level("DEBUG", "INFO")
    with pytest.raises(ValueError):
        should_emit_level("AUDIT", "ERROR")


def test_json_schema_accepts_shared_fixture() -> None:
    contract_dir = _REPOSITORY_ROOT / "contracts" / "logging" / "v2"
    schema = json.loads((contract_dir / "log-event.schema.json").read_text())
    fixtures = json.loads((contract_dir / "testdata" / "valid-events.json").read_text())
    validator = jsonschema.Draft202012Validator(
        schema,
        format_checker=jsonschema.FormatChecker(),
    )
    for fixture in fixtures:
        validator.validate(fixture)


def test_json_schema_rejects_all_shared_invalid_fixtures() -> None:
    contract_dir = _REPOSITORY_ROOT / "contracts" / "logging" / "v2"
    schema = json.loads((contract_dir / "log-event.schema.json").read_text())
    fixtures = json.loads(
        (contract_dir / "testdata" / "invalid-events.json").read_text()
    )
    validator = jsonschema.Draft202012Validator(
        schema,
        format_checker=jsonschema.FormatChecker(),
    )
    for fixture in fixtures:
        with pytest.raises(jsonschema.ValidationError):
            validator.validate(fixture["payload"])


def test_event_rejects_untrimmed_service_and_preserves_message_whitespace() -> None:
    with pytest.raises(ValidationError):
        LogEvent(service=" backend ", message="message")
    event = LogEvent(service="backend", message=" message\n")
    assert event.message == " message\n"


def test_wire_decoder_rejects_nonstandard_json_and_timestamp() -> None:
    fixture = {
        "event_id": "b6dd42df-660d-4aca-a712-6ce1c85ceafd",
        "timestamp": "2026-08-01T12:00:00Z",
        "kind": "LOG",
        "level": "INFO",
        "service": "contract-test",
        "message": "valid message",
        "trace_id": "trace-123",
        "metadata": {},
    }
    lowercase_timestamp = fixture | {"timestamp": "2026-08-01T12:00:00z"}
    with pytest.raises(ValueError):
        decode_event(json.dumps(lowercase_timestamp))
    nonstandard_number = fixture | {"metadata": {"value": float("nan")}}
    with pytest.raises(ValueError):
        decode_event(json.dumps(nonstandard_number))


def test_service_auth_schema_accepts_rotating_tokens() -> None:
    contract_dir = _REPOSITORY_ROOT / "contracts" / "logging" / "v2"
    schema = json.loads((contract_dir / "service-auth.schema.json").read_text())
    jsonschema.Draft202012Validator(schema).validate(
        {
            "services": {
                "orders": ["a" * 32, "b" * 32],
            }
        }
    )


def test_dead_letter_schema_and_python_model_accept_shared_fixture() -> None:
    contract_dir = _REPOSITORY_ROOT / "contracts" / "logging" / "v2"
    schema = json.loads((contract_dir / "dead-letter.schema.json").read_text())
    fixture = json.loads(
        (contract_dir / "testdata" / "valid-dead-letter.json").read_text()
    )
    jsonschema.Draft202012Validator(
        schema,
        format_checker=jsonschema.FormatChecker(),
    ).validate(fixture)
    dead_letter = DeadLetter.model_validate(fixture)
    assert dead_letter.source_offset == 42


def test_oversize_dead_letter_schema_and_python_model_accept_shared_fixture() -> None:
    contract_dir = _REPOSITORY_ROOT / "contracts" / "logging" / "v2"
    schema = json.loads((contract_dir / "dead-letter-v2.schema.json").read_text())
    fixture = json.loads(
        (contract_dir / "testdata" / "valid-dead-letter-v2.json").read_text()
    )
    jsonschema.Draft202012Validator(
        schema,
        format_checker=jsonschema.FormatChecker(),
    ).validate(fixture)
    dead_letter = OversizeDeadLetter.model_validate(fixture)
    assert dead_letter.source_offset == 43
    assert dead_letter.content_omitted is True


def test_contract_limits_match_python_constants() -> None:
    limits_path = _REPOSITORY_ROOT / "contracts" / "logging" / "v2" / "limits.json"
    limits = json.loads(limits_path.read_text())
    assert limits == {
        "schema_version": "v2",
        "max_event_json_bytes": MAX_EVENT_JSON_BYTES,
        "max_http_body_bytes": MAX_HTTP_BODY_BYTES,
        "max_kafka_key_value_bytes": MAX_KAFKA_KEY_VALUE_BYTES,
        "max_kafka_message_bytes": MAX_KAFKA_MESSAGE_BYTES,
    }


def test_openapi_document_is_valid_yaml() -> None:
    openapi_path = _REPOSITORY_ROOT / "contracts" / "logging" / "v2" / "openapi.yaml"
    document = yaml.safe_load(openapi_path.read_text())
    assert document["openapi"] == "3.1.0"
    assert "/v2/log-events/batch" in document["paths"]
    specification, base_uri = read_from_filename(str(openapi_path))
    validate(specification, base_uri=base_uri)
