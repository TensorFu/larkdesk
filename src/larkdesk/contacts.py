"""Parse and list 联系人 from the reused desktop session."""

from __future__ import annotations

import json
import sqlite3
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Iterable

from larkdesk.gateway import GatewayError, decode_fields, post_command
from larkdesk.session import AuthMaterial, SessionError, default_larkshell, load_session

CMD_PULL_CONTACTS_V2 = 1100310


class ContactsError(RuntimeError):
    """Contact list could not be produced."""


@dataclass(frozen=True)
class Contact:
    name: str
    id: str
    extra: dict[str, Any] | None = None

    def as_dict(self) -> dict[str, Any]:
        out: dict[str, Any] = {"name": self.name, "id": self.id}
        if self.extra:
            for key, value in self.extra.items():
                if key not in out and value not in (None, ""):
                    out[key] = value
        return out


def _unwrap_org_val(raw: object) -> object:
    if isinstance(raw, dict) and "orgVal" in raw:
        inner = raw["orgVal"]
        if isinstance(inner, str):
            try:
                return json.loads(inner)
            except json.JSONDecodeError:
                return inner
        return inner
    if isinstance(raw, str):
        try:
            return _unwrap_org_val(json.loads(raw))
        except json.JSONDecodeError:
            return raw
    return raw


def _looks_like_department(obj: dict) -> bool:
    return bool(obj.get("parentId") or obj.get("parent_id")) and "avatarKey" not in obj and "avatar_key" not in obj


def _contact_from_mapping(obj: dict) -> Contact | None:
    name = obj.get("localizedName") or obj.get("nameWithAnotherName") or obj.get("localized_name") or obj.get("name") or obj.get("displayName")
    ident = (
        obj.get("open_id")
        or obj.get("openId")
        or obj.get("userId")
        or obj.get("user_id")
        or obj.get("chatterId")
        or obj.get("chatter_id")
        or obj.get("id")
    )
    if not name or ident is None:
        return None
    name_s = str(name).strip()
    ident_s = str(ident).strip()
    if not name_s or not ident_s:
        return None
    if _looks_like_department(obj):
        return None
    extra: dict[str, Any] = {}
    dept = obj.get("departmentName") or obj.get("department")
    if dept:
        extra["department"] = dept
    if obj.get("tenantId"):
        extra["tenant_id"] = obj["tenantId"]
    return Contact(name=name_s, id=ident_s, extra=extra or None)


def parse_contact_payload(payload: Any) -> list[Contact]:
    """Turn a desktop/API contact payload into name+id entries.

    Accepts the Feishu org-cache shape, a GetContacts-like JSON object, or a
    list of user dicts. Does not invent entries.
    """
    found: list[Contact] = []
    seen: set[str] = set()

    def add(contact: Contact | None) -> None:
        if contact is None or contact.id in seen:
            return
        seen.add(contact.id)
        found.append(contact)

    def walk(node: Any) -> None:
        if isinstance(node, dict):
            entity = node.get("entity")
            if isinstance(entity, dict):
                add(_contact_from_mapping(entity))
            add(_contact_from_mapping(node))
            for key, value in node.items():
                if key in {"users", "contacts", "entries", "dataSource", "chatters", "items", "data"}:
                    walk(value)
                elif isinstance(value, (dict, list)):
                    walk(value)
        elif isinstance(node, list):
            for item in node:
                walk(item)
        elif isinstance(node, str):
            try:
                walk(json.loads(node))
            except json.JSONDecodeError:
                return

    walk(_unwrap_org_val(payload))
    return found


def load_local_contacts(larkshell: Path, user_id: str | None = None) -> list[Contact]:
    db = larkshell / "persistent_storage.db"
    if not db.is_file():
        raise ContactsError(f"missing desktop session store: {db}")
    con = sqlite3.connect(f"file:{db}?mode=ro", uri=True)
    contacts: list[Contact] = []
    try:
        tables = [
            row[0]
            for row in con.execute(
                "SELECT name FROM sqlite_master WHERE type='table' AND name LIKE 'kv_%'"
            )
        ]
        for table in tables:
            cols = {c[1] for c in con.execute(f"PRAGMA table_info({table})")}
            if "key" not in cols or "value" not in cols:
                continue
            for key, value in con.execute(f"SELECT key, value FROM {table}"):
                if not isinstance(value, str):
                    continue
                if not any(
                    token in value
                    for token in ("localizedName", "nameWithAnotherName", "avatarKey", "open_id")
                ):
                    continue
                try:
                    payload = json.loads(value)
                except json.JSONDecodeError:
                    continue
                contacts.extend(parse_contact_payload(payload))
    except sqlite3.Error as exc:
        raise ContactsError(f"cannot read desktop contact cache: {exc}") from exc
    finally:
        con.close()
    return _dedupe(contacts)


def _dedupe(contacts: Iterable[Contact]) -> list[Contact]:
    seen: set[str] = set()
    out: list[Contact] = []
    for contact in contacts:
        if contact.id in seen:
            continue
        seen.add(contact.id)
        out.append(contact)
    return out


def _utf8(data: bytes) -> str | None:
    try:
        text = data.decode("utf-8")
    except UnicodeDecodeError:
        return None
    if not text or not text.isprintable():
        return None
    return text


def parse_pull_contacts_v2(payload: bytes) -> list[Contact]:
    """Parse PULL_CONTACTS_V2 inner protobuf into name+id contacts."""
    contacts: list[Contact] = []
    for field, kind, value in decode_fields(payload):
        if field != 4 or kind != "bytes" or not isinstance(value, (bytes, bytearray)):
            continue
        name = ""
        ident = ""
        extra: dict[str, Any] = {}
        for inner_f, inner_k, inner_v in decode_fields(bytes(value)):
            if inner_k != "bytes" or not isinstance(inner_v, (bytes, bytearray)):
                continue
            nested = decode_fields(bytes(inner_v))
            by_f = {
                nf: nv
                for nf, nk, nv in nested
                if nk == "bytes" and isinstance(nv, (bytes, bytearray))
            }
            if inner_f == 1:
                name = _utf8(bytes(by_f.get(2, b""))) or name
                ident = _utf8(bytes(by_f.get(6, b""))) or ident
                tenant = _utf8(bytes(by_f.get(4, b"")))
                if tenant:
                    extra["tenant"] = tenant
            elif inner_f == 3:
                ident = ident or (_utf8(bytes(by_f.get(1, b""))) or "")
                name = name or (_utf8(bytes(by_f.get(2, b""))) or "")
        if name and ident:
            contacts.append(Contact(name=name, id=ident, extra=extra or None))
    return _dedupe(contacts)


def fetch_remote_contacts(auth: AuthMaterial, timeout: float = 20.0) -> list[Contact]:
    """Send the PULL_CONTACTS_V2 gateway packet and parse the contact list."""
    try:
        payload = post_command(auth, CMD_PULL_CONTACTS_V2, timeout=timeout)
    except GatewayError as exc:
        raise ContactsError(str(exc)) from exc
    parsed = parse_pull_contacts_v2(payload)
    if not parsed:
        raise ContactsError("PULL_CONTACTS_V2 returned no contacts")
    return parsed


def list_contacts(
    larkshell: Path | None = None,
    *,
    auth: AuthMaterial | None = None,
    cookie_passwords: Iterable[tuple[str, str]] | None = None,
) -> tuple[AuthMaterial, list[Contact], str]:
    root = larkshell or default_larkshell()
    session = auth or load_session(root, cookie_passwords=cookie_passwords)
    contacts = load_local_contacts(root, session.identity.user_id)

    self_contact = Contact(name=session.identity.name, id=session.identity.user_id)
    contacts = _dedupe([self_contact, *contacts])
    source = "desktop-session"

    if session.cookies and session.identity.domain != "fixture.feishu.cn":
        try:
            remote = fetch_remote_contacts(session)
            contacts = _dedupe([*remote, *contacts])
            source = "pull_contacts_v2"
        except ContactsError:
            pass

    if not contacts:
        raise ContactsError(
            "desktop session produced an empty contact list (auth/permission failure)"
        )
    return session, contacts, source
