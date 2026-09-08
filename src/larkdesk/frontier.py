"""Frontier WebSocket used by the Feishu web/desktop client for inbound push."""

from __future__ import annotations

import gzip
import hashlib
import json
import ssl
import uuid
from typing import AsyncIterator
from urllib.error import HTTPError, URLError
from urllib.parse import urlencode
from urllib.request import Request, build_opener

from larkdesk.gateway import _proxy_handler, decode_fields, decode_packet
from larkdesk.session import AuthMaterial

APPKEY_SAAS = "5f45da0e6c7a17dcba80494ef0ab9b21"
ACCESS_KEY_SALT = "f8a69f1719916z"
PASSPORT_TICKET = "https://passport.feishu.cn/suite/passport/frontier_ticket/"
FRONTIER_HOST = "msg-frontier.feishu.cn"


class FrontierError(RuntimeError):
    """Frontier ticket or websocket failed."""


def maybe_gunzip(data: bytes) -> bytes:
    if len(data) >= 2 and data[:2] == b"\x1f\x8b":
        try:
            return gzip.decompress(data)
        except OSError:
            return data
    return data


def access_key(device_id: str, *, appkey: str = APPKEY_SAAS) -> str:
    raw = f"2{appkey}{device_id}{ACCESS_KEY_SALT}".encode("utf-8")
    return hashlib.md5(raw).hexdigest()


def fetch_ticket(auth: AuthMaterial) -> tuple[str, str]:
    header = auth.cookie_header()
    if "session=" not in header and "osession=" not in header and "sl_session=" not in header:
        raise FrontierError("no decrypted session cookie for frontier")
    url = PASSPORT_TICKET + "?" + urlencode({"local_device_id": ""})
    req = Request(
        url,
        headers={
            "User-Agent": "larkdesk/0.1",
            "Cookie": header,
            "Referer": "https://www.feishu.cn/messenger/",
            "Origin": "https://www.feishu.cn",
        },
        method="GET",
    )
    handlers = []
    proxy = _proxy_handler()
    if proxy is not None:
        handlers.append(proxy)
    opener = build_opener(*handlers)
    try:
        with opener.open(req, timeout=20) as resp:
            body = json.loads(resp.read().decode("utf-8", "replace"))
    except HTTPError as exc:
        raise FrontierError(f"frontier ticket HTTP {exc.code}") from exc
    except URLError as exc:
        raise FrontierError(f"frontier ticket {exc.reason}") from exc
    except json.JSONDecodeError as exc:
        raise FrontierError("frontier ticket was not JSON") from exc
    if not isinstance(body, dict):
        raise FrontierError("frontier ticket unexpected body")
    device_id = str(body.get("device_id") or body.get("deviceId") or "")
    ticket = str(body.get("ticket") or "")
    if not ticket:
        raise FrontierError("frontier ticket missing")
    return device_id or "0", ticket


def build_ws_url(device_id: str, ticket: str) -> str:
    qs = urlencode(
        {
            "access_key": access_key(device_id),
            "aid": "1",
            "ticket": ticket,
            "device_id": device_id,
            "fpid": "2",
            "accept_encoding": "gzip",
            "request_id": str(uuid.uuid4()),
        }
    )
    return f"wss://{FRONTIER_HOST}/ws/v2?{qs}"


def decode_frontier_headers(raw: bytes) -> dict[str, str]:
    headers: dict[str, str] = {}
    for field, kind, value in decode_fields(raw):
        if field != 5 or kind != "bytes" or not isinstance(value, (bytes, bytearray)):
            continue
        name = ""
        val = ""
        for inf, ink, inv in decode_fields(bytes(value)):
            if inf == 1 and ink == "bytes" and isinstance(inv, (bytes, bytearray)):
                name = bytes(inv).decode("utf-8", "replace")
            elif inf == 2 and ink == "bytes" and isinstance(inv, (bytes, bytearray)):
                val = bytes(inv).decode("utf-8", "replace")
        if name:
            headers[name] = val
    return headers


def decode_frontier_body(raw: bytes) -> bytes:
    body = b""
    for field, kind, value in decode_fields(raw):
        if field == 8 and kind == "bytes" and isinstance(value, (bytes, bytearray)):
            body = bytes(value)
    return maybe_gunzip(body)


def decode_frontier_packet(raw: bytes) -> dict[str, object]:
    """Decode one websocket frame into an improto.Packet dict (cmd/payload/headers)."""
    raw = maybe_gunzip(raw)
    headers = decode_frontier_headers(raw)
    body = decode_frontier_body(raw)
    pkt: dict[str, object] = {"headers": headers}
    if body:
        encoding = headers.get("content-encoding") or headers.get("Content-Encoding") or ""
        if encoding == "gzip":
            body = maybe_gunzip(body)
        inner = decode_packet(body)
        pkt.update(inner)
    return pkt


async def iter_frontier_frames(auth: AuthMaterial) -> AsyncIterator[bytes]:
    """Yield raw websocket frames, reconnecting on drop."""
    import asyncio

    import websockets

    ssl_ctx = ssl.create_default_context()
    while True:
        try:
            device_id, ticket = fetch_ticket(auth)
            url = build_ws_url(device_id, ticket)
            async with websockets.connect(
                url,
                ssl=ssl_ctx,
                max_size=8 * 1024 * 1024,
                origin="https://www.feishu.cn",
                extra_headers={"Cookie": auth.cookie_header()},
                user_agent_header="larkdesk/0.1",
            ) as ws:
                async for msg in ws:
                    raw = msg if isinstance(msg, bytes) else str(msg).encode("utf-8", "replace")
                    yield maybe_gunzip(raw)
        except asyncio.CancelledError:
            raise
        except Exception:
            await asyncio.sleep(3)
