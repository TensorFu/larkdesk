"""HTTP /im/gateway/ Packet envelope used by the Feishu web/desktop client."""

from __future__ import annotations

import os
import uuid
from urllib.error import HTTPError, URLError
from urllib.request import ProxyHandler, Request, build_opener

from larkdesk.session import AuthMaterial


class GatewayError(RuntimeError):
    """Gateway packet could not be sent or decoded."""


def _uvarint(n: int) -> bytes:
    n &= (1 << 64) - 1
    out = bytearray()
    while n > 0x7F:
        out.append((n & 0x7F) | 0x80)
        n >>= 7
    out.append(n)
    return bytes(out)


def _key(field: int, wire: int) -> bytes:
    return _uvarint((field << 3) | wire)


def encode_int32(field: int, n: int) -> bytes:
    return _key(field, 0) + _uvarint(n)


def encode_bytes(field: int, data: bytes) -> bytes:
    return _key(field, 2) + _uvarint(len(data)) + data


def encode_string(field: int, text: str) -> bytes:
    return encode_bytes(field, text.encode("utf-8"))


def encode_packet(cmd: int, payload: bytes = b"", cid: str | None = None) -> tuple[bytes, str]:
    """improto.Packet: payload_type=PB2, cmd, payload, cid."""
    cid = cid or str(uuid.uuid4())
    body = (
        encode_int32(2, 1)
        + encode_int32(3, cmd)
        + encode_bytes(5, payload)
        + encode_string(6, cid)
    )
    return body, cid


def decode_varint(buf: bytes, i: int) -> tuple[int, int]:
    shift = 0
    n = 0
    while True:
        if i >= len(buf):
            raise GatewayError("truncated varint")
        b = buf[i]
        i += 1
        n |= (b & 0x7F) << shift
        if b < 0x80:
            return n, i
        shift += 7
        if shift > 63:
            raise GatewayError("varint too long")


def decode_fields(buf: bytes) -> list[tuple[int, str, int | bytes]]:
    i = 0
    fields: list[tuple[int, str, int | bytes]] = []
    while i < len(buf):
        try:
            k, i = decode_varint(buf, i)
        except GatewayError:
            break
        field, wire = k >> 3, k & 7
        if field <= 0:
            break
        if wire == 0:
            v, i = decode_varint(buf, i)
            fields.append((field, "varint", v))
        elif wire == 2:
            ln, i = decode_varint(buf, i)
            if i + ln > len(buf):
                break
            fields.append((field, "bytes", buf[i : i + ln]))
            i += ln
        elif wire == 1:
            if i + 8 > len(buf):
                break
            fields.append((field, "fixed64", buf[i : i + 8]))
            i += 8
        elif wire == 5:
            if i + 4 > len(buf):
                break
            fields.append((field, "fixed32", buf[i : i + 4]))
            i += 4
        else:
            break
    return fields


def decode_packet(buf: bytes) -> dict[str, object]:
    fields = decode_fields(buf)
    out: dict[str, object] = {}
    for field, _kind, value in fields:
        if field == 2:
            out["payload_type"] = value
        elif field == 3:
            out["cmd"] = value
        elif field == 4:
            out["status"] = value
        elif field == 5 and isinstance(value, (bytes, bytearray)):
            out["payload"] = bytes(value)
        elif field == 6 and isinstance(value, (bytes, bytearray)):
            out["cid"] = value.decode("utf-8", "replace")
    return out


def _proxy_handler() -> ProxyHandler | None:
    proxy = os.environ.get("LARKDESK_PROXY")
    if proxy == "":
        return None
    if not proxy:
        proxy = os.environ.get("HTTPS_PROXY") or os.environ.get("https_proxy") or "http://127.0.0.1:1082"
    return ProxyHandler({"http": proxy, "https": proxy})


def post_packet(
    auth: AuthMaterial,
    cmd: int,
    payload: bytes = b"",
    *,
    timeout: float = 20.0,
) -> dict[str, object]:
    """POST one Packet-wrapped command. Returns decoded Packet dict."""
    header = auth.cookie_header()
    if "session=" not in header and "osession=" not in header and "sl_session=" not in header:
        raise GatewayError("no decrypted session cookie for gateway")

    body, cid = encode_packet(cmd, payload)
    url = "https://internal-api-lark-api.feishu.cn/im/gateway/"
    headers = {
        "User-Agent": "larkdesk/0.1",
        "Content-Type": "application/x-protobuf",
        "Accept": "*/*",
        "X-Command": str(cmd),
        "X-Request-Id": cid,
        "X-Source": "web",
        "Origin": "https://www.feishu.cn",
        "Referer": "https://www.feishu.cn/messenger/",
        "Cookie": header,
    }
    handlers = []
    proxy = _proxy_handler()
    if proxy is not None:
        handlers.append(proxy)
    opener = build_opener(*handlers)
    req = Request(url, data=body, headers=headers, method="POST")
    try:
        with opener.open(req, timeout=timeout) as resp:
            raw = resp.read()
    except HTTPError as exc:
        raise GatewayError(f"gateway HTTP {exc.code} cmd={cmd}") from exc
    except URLError as exc:
        raise GatewayError(f"gateway {exc.reason} cmd={cmd}") from exc
    return decode_packet(raw)


def post_command(
    auth: AuthMaterial,
    cmd: int,
    payload: bytes = b"",
    *,
    timeout: float = 20.0,
) -> bytes:
    """POST one Packet-wrapped command to /im/gateway/. Returns inner payload."""
    pkt = post_packet(auth, cmd, payload, timeout=timeout)
    inner = pkt.get("payload")
    if isinstance(inner, (bytes, bytearray)):
        return bytes(inner)
    return b""
