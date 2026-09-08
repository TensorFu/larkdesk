"""Send a text message: resolve person, get-or-create P2P, PUT_MESSAGE."""

from __future__ import annotations

import uuid
from dataclasses import dataclass

from larkdesk.contacts import Contact
from larkdesk.gateway import (
    GatewayError,
    decode_fields,
    encode_bytes,
    encode_int32,
    encode_string,
    post_command,
)
from larkdesk.search import SearchError, search_contacts
from larkdesk.session import AuthMaterial, load_session

CMD_PUT_P2P_CHATS = 50
CMD_PUT_MESSAGE = 5
MSG_TYPE_TEXT = 4
TAG_TEXT = 1


class SendError(RuntimeError):
    """Message could not be sent."""


@dataclass(frozen=True)
class SendPlan:
    to: Contact
    chat_id: str
    text: str


def _utf8(data: bytes) -> str | None:
    try:
        text = data.decode("utf-8")
    except UnicodeDecodeError:
        return None
    if not text or not text.isprintable():
        return None
    return text


def resolve_person(who: str, *, auth: AuthMaterial) -> Contact:
    who = who.strip()
    try:
        hits = search_contacts(who, auth=auth)[1]
    except SearchError as exc:
        raise SendError(str(exc)) from exc
    if who.isdigit() and len(who) >= 10:
        for hit in hits:
            if hit.id == who:
                return hit
        return Contact(name=who, id=who)
    exact = [c for c in hits if c.name == who]
    pool = exact or hits
    if not pool:
        raise SendError(f"no contact matched {who!r}")
    if len(pool) > 1:
        names = ", ".join(f"{c.name}({c.id})" for c in pool[:8])
        raise SendError(f"multiple contacts for {who!r}: {names}")
    return pool[0]


def put_p2p_chat(auth: AuthMaterial, user_id: str) -> str:
    """PUT_P2P_CHATS (50): get or create P2P chat. Returns chat id."""
    try:
        payload = post_command(auth, CMD_PUT_P2P_CHATS, encode_string(1, user_id))
    except GatewayError as exc:
        raise SendError(str(exc)) from exc
    fields = decode_fields(payload)
    if not fields:
        raise SendError("PUT_P2P_CHATS empty response")
    for f, k, v in fields:
        if f == 1 and k == "bytes" and isinstance(v, (bytes, bytearray)):
            inner = decode_fields(bytes(v))
            for inf, ink, inv in inner:
                if inf == 1 and ink == "bytes" and isinstance(inv, (bytes, bytearray)):
                    chat_id = _utf8(bytes(inv))
                    if chat_id and chat_id.isdigit():
                        return chat_id
    raise SendError("PUT_P2P_CHATS missing chat id")


def encode_rich_text(text: str) -> bytes:
    eid = str(uuid.uuid4())
    prop = encode_bytes(1, encode_string(1, text))
    element = encode_int32(1, TAG_TEXT) + encode_bytes(3, prop)
    dictionary = encode_string(1, eid) + encode_bytes(2, element)
    elements = encode_bytes(1, dictionary)
    return encode_string(1, eid) + encode_string(2, text) + encode_bytes(3, elements)


def encode_put_message(chat_id: str, text: str) -> bytes:
    """PUT_MESSAGE (5): type=TEXT, content.richText, chatId."""
    content = encode_bytes(1, encode_rich_text(text))
    return encode_int32(1, MSG_TYPE_TEXT) + encode_bytes(2, content) + encode_string(3, chat_id)


def parse_put_message_response(payload: bytes) -> str:
    fields = decode_fields(payload)
    for f, k, v in fields:
        if f == 1 and k == "bytes" and isinstance(v, (bytes, bytearray)):
            inner = decode_fields(bytes(v))
            for inf, ink, inv in inner:
                if inf == 1 and ink == "bytes" and isinstance(inv, (bytes, bytearray)):
                    mid = _utf8(bytes(inv))
                    if mid:
                        return mid
    return ""


def plan_send(who: str, text: str, *, auth: AuthMaterial | None = None) -> tuple[AuthMaterial, SendPlan]:
    text = text.strip()
    if not text:
        raise SendError("empty message")
    session = auth or load_session()
    if not session.cookies:
        raise SendError("no decrypted session cookie")
    person = resolve_person(who, auth=session)
    chat_id = put_p2p_chat(session, person.id)
    return session, SendPlan(to=person, chat_id=chat_id, text=text)


def send_text(who: str, text: str, *, auth: AuthMaterial | None = None, dry_run: bool = False) -> dict[str, object]:
    session, plan = plan_send(who, text, auth=auth)
    out: dict[str, object] = {
        "to": plan.to.as_dict(),
        "chat_id": plan.chat_id,
        "text": plan.text,
        "dry_run": dry_run,
    }
    if dry_run:
        return out
    try:
        raw = post_command(session, CMD_PUT_MESSAGE, encode_put_message(plan.chat_id, plan.text))
    except GatewayError as exc:
        raise SendError(str(exc)) from exc
    if plan.text.encode("utf-8") not in raw:
        raise SendError("PUT_MESSAGE succeeded but body did not echo the text")
    mid = parse_put_message_response(raw)
    out["message_id"] = mid
    out["sent"] = True
    return out
