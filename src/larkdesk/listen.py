"""Listen for inbound text pushes on the desktop-session frontier socket."""

from __future__ import annotations

import json
import shlex
import subprocess
import sys
from collections import deque
from dataclasses import dataclass
from typing import Callable

from larkdesk.contacts import Contact, list_contacts
from larkdesk.frontier import decode_frontier_packet, iter_frontier_frames
from larkdesk.gateway import decode_fields
from larkdesk.search import SearchError
from larkdesk.send import MSG_TYPE_TEXT, SendError, resolve_person
from larkdesk.session import AuthMaterial, load_session

CMD_PUSH_MESSAGES = 6
CMD_PUSH_MESSAGES_V2 = 5065
_SEEN_MAX = 512


class ListenError(RuntimeError):
    """Inbound listen could not start or decode."""


@dataclass(frozen=True)
class InboundEvent:
    ts: int
    chat_id: str
    message_id: str
    from_id: str
    from_name: str
    text: str
    self: bool
    cmd: int = CMD_PUSH_MESSAGES

    def as_dict(self) -> dict[str, object]:
        frm: dict[str, str] = {"id": self.from_id}
        if self.from_name:
            frm["name"] = self.from_name
        return {
            "ts": self.ts,
            "chat_id": self.chat_id,
            "message_id": self.message_id,
            "from": frm,
            "text": self.text,
            "self": self.self,
        }


def _utf8(data: bytes) -> str | None:
    try:
        text = data.decode("utf-8")
    except UnicodeDecodeError:
        return None
    return text if text else None


def _printable(text: str) -> bool:
    return bool(text) and all(ch.isprintable() or ch in "\n\r\t" for ch in text)


def extract_plain_text(buf: bytes) -> str:
    """Pull RichText.innerText, else the longest non-id string in the tree."""
    inner: list[str] = []
    strings: list[str] = []

    def walk(raw: bytes) -> None:
        fields = decode_fields(raw)
        tags = {f for f, _k, _v in fields}
        for field, kind, value in fields:
            if kind != "bytes" or not isinstance(value, (bytes, bytearray)):
                continue
            chunk = bytes(value)
            text = _utf8(chunk)
            if text and _printable(text):
                strings.append(text)
                if field == 2 and 3 in tags and text.strip() and not text.isdigit():
                    inner.append(text)
            if chunk:
                walk(chunk)

    walk(buf)
    for text in inner:
        if text.strip():
            return text
    cands = [s for s in strings if not s.isdigit() and _printable(s) and len(s) >= 1]
    return max(cands, key=len) if cands else ""


def _message_from_fields(buf: bytes) -> dict[str, object] | None:
    fields = decode_fields(buf)
    if not fields:
        return None
    by: dict[int, list] = {}
    for field, kind, value in fields:
        by.setdefault(field, []).append((kind, value))
    type_hit = by.get(2, [(None, None)])[0]
    if type_hit[0] != "varint":
        return None
    msg_type = int(type_hit[1])  # type: ignore[arg-type]
    mid = ""
    from_id = ""
    chat_id = ""
    ts = 0
    content = b""
    if 1 in by and by[1][0][0] == "bytes":
        mid = _utf8(bytes(by[1][0][1])) or ""  # type: ignore[arg-type]
    if 3 in by and by[3][0][0] == "bytes":
        from_id = _utf8(bytes(by[3][0][1])) or ""  # type: ignore[arg-type]
    if 4 in by and by[4][0][0] == "varint":
        ts = int(by[4][0][1])  # type: ignore[arg-type]
    if 5 in by and by[5][0][0] == "bytes":
        content = bytes(by[5][0][1])  # type: ignore[arg-type]
    if 10 in by and by[10][0][0] == "bytes":
        chat_id = _utf8(bytes(by[10][0][1])) or ""  # type: ignore[arg-type]
    return {
        "id": mid,
        "type": msg_type,
        "from_id": from_id,
        "ts": ts,
        "content": content,
        "chat_id": chat_id,
    }


def parse_push_messages(payload: bytes) -> list[dict[str, object]]:
    """messages.PushMessagesRequest: repeated { id=1, message=2 }."""
    out: list[dict[str, object]] = []
    for field, kind, value in decode_fields(payload):
        if kind != "bytes" or not isinstance(value, (bytes, bytearray)):
            continue
        item = bytes(value)
        direct = _message_from_fields(item)
        if direct and direct.get("type") == MSG_TYPE_TEXT:
            out.append(direct)
            continue
        inner_msg = b""
        inner_id = ""
        for inf, ink, inv in decode_fields(item):
            if inf == 1 and ink == "bytes" and isinstance(inv, (bytes, bytearray)):
                inner_id = _utf8(bytes(inv)) or inner_id
            elif inf == 2 and ink == "bytes" and isinstance(inv, (bytes, bytearray)):
                inner_msg = bytes(inv)
        parsed = _message_from_fields(inner_msg) if inner_msg else None
        if not parsed:
            continue
        if not parsed.get("id"):
            parsed["id"] = inner_id
        if parsed.get("type") == MSG_TYPE_TEXT:
            out.append(parsed)
    return out


def events_from_frame(
    raw: bytes,
    *,
    self_id: str = "",
    names: dict[str, str] | None = None,
) -> list[InboundEvent]:
    pkt = decode_frontier_packet(raw)
    cmd = pkt.get("cmd")
    if cmd not in (CMD_PUSH_MESSAGES, CMD_PUSH_MESSAGES_V2):
        return []
    payload = pkt.get("payload")
    if not isinstance(payload, (bytes, bytearray)):
        return []
    names = names or {}
    events: list[InboundEvent] = []
    for msg in parse_push_messages(bytes(payload)):
        text = extract_plain_text(bytes(msg.get("content") or b""))
        if not text.strip():
            continue
        from_id = str(msg.get("from_id") or "")
        mid = str(msg.get("id") or "")
        events.append(
            InboundEvent(
                ts=int(msg.get("ts") or 0),
                chat_id=str(msg.get("chat_id") or ""),
                message_id=mid,
                from_id=from_id,
                from_name=names.get(from_id, ""),
                text=text,
                self=bool(self_id) and from_id == self_id,
                cmd=int(cmd),
            )
        )
    return events


def _name_map(auth: AuthMaterial) -> dict[str, str]:
    names = {auth.identity.user_id: auth.identity.name}
    try:
        _auth, contacts, _src = list_contacts(auth=auth)
    except Exception:
        return names
    for contact in contacts:
        if contact.id and contact.name:
            names.setdefault(contact.id, contact.name)
    return names


def resolve_listen_filter(who: str, *, auth: AuthMaterial) -> Contact | None:
    who = who.strip()
    if not who:
        return None
    try:
        return resolve_person(who, auth=auth)
    except (SendError, SearchError) as exc:
        raise ListenError(str(exc)) from exc


def _run_hook(command: str, payload: dict[str, object]) -> None:
    data = json.dumps(payload, ensure_ascii=False).encode("utf-8")
    try:
        subprocess.run(shlex.split(command), input=data, check=False)
    except Exception as exc:
        print(f"larkdesk: exec {exc}", file=sys.stderr)


async def listen_loop(
    auth: AuthMaterial,
    *,
    allow_from_id: str = "",
    on_event: Callable[[InboundEvent], None],
    names: dict[str, str] | None = None,
) -> None:
    seen: deque[str] = deque(maxlen=_SEEN_MAX)
    seen_set: set[str] = set()
    async for raw in iter_frontier_frames(auth):
        for event in events_from_frame(raw, self_id=auth.identity.user_id, names=names):
            if allow_from_id and event.from_id != allow_from_id:
                continue
            if event.message_id:
                if event.message_id in seen_set:
                    continue
                if len(seen) == seen.maxlen:
                    old = seen[0]
                    seen_set.discard(old)
                seen.append(event.message_id)
                seen_set.add(event.message_id)
            on_event(event)


def run_listen(
    *,
    from_who: str = "",
    exec_cmd: str = "",
    auth: AuthMaterial | None = None,
) -> None:
    session = auth or load_session()
    if not session.cookies:
        raise ListenError("no decrypted session cookie")
    allow: Contact | None = None
    if from_who.strip():
        allow = resolve_listen_filter(from_who, auth=session)
    names = _name_map(session)

    def emit(event: InboundEvent) -> None:
        payload = event.as_dict()
        sys.stdout.write(json.dumps(payload, ensure_ascii=False) + "\n")
        sys.stdout.flush()
        if exec_cmd:
            _run_hook(exec_cmd, payload)

    print(
        f"larkdesk: listening as {session.identity.name}",
        file=sys.stderr,
    )
    import asyncio

    asyncio.run(
        listen_loop(
            session,
            allow_from_id=allow.id if allow else "",
            on_event=emit,
            names=names,
        )
    )
