"""Bounded synchronous HTTP transport used by the background worker."""

from __future__ import annotations

import json

import httpx

from .contracts import BatchIngestRequest, LogEvent


class BatchTransport:
    """Own an HTTP client and validate the ingestion response contract."""

    def __init__(
        self,
        *,
        base_url: str,
        token: str,
        timeout_seconds: float,
        transport: httpx.BaseTransport | None = None,
    ) -> None:
        self._base_url = base_url.rstrip("/")
        self._token = token
        self._timeout = timeout_seconds
        self._transport = transport
        self._client: httpx.Client | None = None

    @staticmethod
    def encode(events: list[LogEvent]) -> bytes:
        """Encode a canonical compact batch payload."""
        return json.dumps(
            BatchIngestRequest(events=events).model_dump(mode="json"),
            ensure_ascii=False,
            separators=(",", ":"),
        ).encode()

    def send(self, events: list[LogEvent], payload: bytes) -> None:
        """Send one batch and require a matching 200/202 accepted count."""
        response = self._get_client().post(
            f"{self._base_url}/v1/log-events/batch",
            content=payload,
            headers={
                "Content-Type": "application/json",
                "X-Logging-Service-Token": self._token,
            },
        )
        response.raise_for_status()
        body = response.json()
        data = body.get("data") if isinstance(body, dict) else None
        if (
            not isinstance(body, dict)
            or response.status_code not in {200, 202}
            or body.get("code") != response.status_code
            or not isinstance(data, dict)
            or data.get("accepted") != len(events)
        ):
            raise ValueError("logging service returned an invalid accepted count")

    def close(self) -> None:
        """Close the owned HTTP client."""
        if self._client is not None:
            self._client.close()
            self._client = None

    def _get_client(self) -> httpx.Client:
        if self._client is None or self._client.is_closed:
            self._client = httpx.Client(
                timeout=self._timeout,
                transport=self._transport,
                trust_env=False,
            )
        return self._client
