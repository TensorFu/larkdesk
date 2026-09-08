"""Search contacts via UNIVERSAL_SEARCH gateway packet (cmd 11021)."""

from __future__ import annotations

import re
import uuid
from dataclasses import dataclass
from typing import Any

from larkdesk.contacts import Contact
from larkdesk.gateway import (
    GatewayError,
    decode_fields,
    encode_bytes,
    encode_int32,
    encode_string,
    post_command,
)
from larkdesk.session import AuthMaterial, load_session

CMD_UNIVERSAL_SEARCH = 11021
ENTITY_USER = 1
_HIGHLIGHT = re.compile(r"</?h>")


class SearchError(RuntimeError):
    """Contact search could not be produced."""


@dataclass(frozen=True)
class SearchHit:
    contact: Contact
    type: int = ENTITY_USER


def encode_universal_search_request(query: str, *, page_size: int = 20) -> bytes:
    """usearch.UniversalSearchRequest: header.SearchCommonRequestHeader + USER entity."""
    query = query.strip()
    if not query:
        raise SearchError("empty search query")
    entity = encode_int32(1, ENTITY_USER)
    ctx = encode_string(1, "SEARCH_CHATTERS") + encode_bytes(2, entity)
    header = (
        encode_string(1, str(uuid.uuid4()))
        + encode_int32(2, 1)
        + encode_string(3, query)
        + encode_bytes(5, ctx)
        + encode_string(6, "zh_CN")
        + encode_int32(11, page_size)
    )
    return encode_bytes(1, header)


def _utf8(data: bytes) -> str | None:
    try:
        text = data.decode("utf-8")
    except UnicodeDecodeError:
        return None
    if not text or not text.isprintable():
        return None
    return text


def parse_universal_search_response(payload: bytes) -> list[Contact]:
    """Parse UNIVERSAL_SEARCH results into name+id contacts (USER only)."""
    found: list[Contact] = []
    seen: set[str] = set()
    for field, kind, value in decode_fields(payload):
        if field != 2 or kind != "bytes" or not isinstance(value, (bytes, bytearray)):
            continue
        ident = ""
        name = ""
        ent_type = 0
        for f, k, v in decode_fields(bytes(value)):
            if f == 1 and k == "bytes" and isinstance(v, (bytes, bytearray)):
                ident = _utf8(bytes(v)) or ident
            elif f == 2 and k == "varint" and isinstance(v, int):
                ent_type = v
            elif f == 3 and k == "bytes" and isinstance(v, (bytes, bytearray)):
                raw = _utf8(bytes(v)) or ""
                name = _HIGHLIGHT.sub("", raw).strip()
        if ent_type not in (0, ENTITY_USER):
            continue
        if not name or not ident or ident in seen:
            continue
        seen.add(ident)
        found.append(Contact(name=name, id=ident))
    return found


def search_contacts(query: str, *, auth: AuthMaterial | None = None) -> tuple[AuthMaterial, list[Contact]]:
    session = auth or load_session()
    if not session.cookies:
        raise SearchError("no decrypted session cookie for search")
    payload = encode_universal_search_request(query)
    try:
        raw = post_command(session, CMD_UNIVERSAL_SEARCH, payload)
    except GatewayError as exc:
        raise SearchError(str(exc)) from exc
    return session, parse_universal_search_response(raw)
