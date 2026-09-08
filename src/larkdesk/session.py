"""Load auth material from an already-logged-in Lark.app / 飞书 desktop session."""

from __future__ import annotations

import hashlib
import json
import os
import sqlite3
import subprocess
from dataclasses import dataclass, field
from hashlib import pbkdf2_hmac
from pathlib import Path
from typing import Iterable

from Crypto.Cipher import AES

DEFAULT_LARKSHELL = Path.home() / "Library/Application Support/LarkShell"
PROFILE_KEY_PREFIX = "CURRENT_PROFILE_INFO__"
TENANT_KEY_PREFIX = "CURRENT_TENANT_INFO__"
SESSION_COOKIE_NAMES = ("session", "osession", "sl_session", "session_list")

# Chromium cookie DB v24 prepends SHA-256(host_key) to the decrypted value.
CHROMIUM_HASH_PREFIX_VERSION = 24


class SessionError(RuntimeError):
    """Desktop session is missing, unreadable, or produced no identity."""


@dataclass(frozen=True)
class Identity:
    user_id: str
    name: str
    tenant_id: str = ""
    tenant_name: str = ""
    domain: str = ""


@dataclass
class AuthMaterial:
    """HTTP-layer auth derived from the desktop Chromium cookie store."""

    identity: Identity
    cookies: dict[str, str] = field(default_factory=dict)
    cookie_password_source: str = ""

    def cookie_header(self) -> str:
        parts = [f"{name}={value}" for name, value in self.cookies.items() if value]
        return "; ".join(parts)


def default_larkshell() -> Path:
    override = os.environ.get("LARKDESK_LARKSHELL")
    if override:
        return Path(override).expanduser()
    return DEFAULT_LARKSHELL


def user_dir_hash(user_id: str) -> str:
    return hashlib.md5(user_id.encode("utf-8")).hexdigest()


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


def _open_ro(path: Path) -> sqlite3.Connection:
    return sqlite3.connect(f"file:{path}?mode=ro", uri=True)


def load_identity(larkshell: Path) -> Identity:
    db = larkshell / "persistent_storage.db"
    if not db.is_file():
        raise SessionError(f"missing desktop session store: {db}")

    try:
        con = _open_ro(db)
    except sqlite3.Error as exc:
        raise SessionError(f"cannot open desktop session store: {exc}") from exc

    try:
        tables = [
            row[0]
            for row in con.execute(
                "SELECT name FROM sqlite_master WHERE type='table' AND name LIKE 'kv_%'"
            )
        ]
        profile: dict = {}
        tenant: dict = {}
        profile_key_user_id = ""
        for table in tables:
            cols = {c[1] for c in con.execute(f"PRAGMA table_info({table})")}
            if "key" not in cols or "value" not in cols:
                continue
            rows = con.execute(f"SELECT key, value FROM {table}").fetchall()
            for key, value in rows:
                if not isinstance(key, str) or not isinstance(value, str):
                    continue
                if key.startswith(PROFILE_KEY_PREFIX):
                    if not profile_key_user_id:
                        profile_key_user_id = key[len(PROFILE_KEY_PREFIX) :]
                    if not profile:
                        unwrapped = _unwrap_org_val(json.loads(value))
                        if isinstance(unwrapped, dict):
                            profile = unwrapped
                if key.startswith(TENANT_KEY_PREFIX) and not tenant:
                    unwrapped = _unwrap_org_val(json.loads(value))
                    if isinstance(unwrapped, dict):
                        tenant = unwrapped
                if key == "passportTenantInfo" and not tenant:
                    unwrapped = _unwrap_org_val(json.loads(value))
                    if isinstance(unwrapped, dict):
                        data = unwrapped.get("data", unwrapped)
                        if isinstance(data, dict):
                            tenant = data
    except (sqlite3.Error, json.JSONDecodeError, TypeError, ValueError) as exc:
        raise SessionError(f"desktop session store is unreadable: {exc}") from exc
    finally:
        con.close()

    user_id = str(tenant.get("userId") or tenant.get("user_id") or profile_key_user_id or "")

    name = str(
        profile.get("localizedName")
        or profile.get("nameWithAnotherName")
        or profile.get("name")
        or ""
    )
    if not user_id or not name:
        raise SessionError(
            "no logged-in Feishu desktop identity (open Lark.app and sign in first)"
        )
    return Identity(
        user_id=user_id,
        name=name,
        tenant_id=str(profile.get("tenantId") or tenant.get("tenantId") or ""),
        tenant_name=str(tenant.get("description") or tenant.get("tenantName") or ""),
        domain=str(tenant.get("suiteFullDomain") or ""),
    )


def chromium_db_version(cookies_path: Path) -> int:
    con = _open_ro(cookies_path)
    try:
        row = con.execute("SELECT value FROM meta WHERE key='version'").fetchone()
        return int(row[0]) if row else 0
    except sqlite3.Error:
        return 0
    finally:
        con.close()


def _unpad_pkcs7(data: bytes) -> bytes:
    if not data:
        return data
    pad = data[-1]
    if pad < 1 or pad > 16 or not data.endswith(bytes([pad]) * pad):
        return data
    return data[:-pad]


def derive_chromium_key(password: str, iterations: int = 1003) -> bytes:
    return pbkdf2_hmac("sha1", password.encode("utf-8"), b"saltysalt", iterations, 16)


def decrypt_chromium_value(encrypted_value: bytes, password: str, *, host_key: str = "", db_version: int = 0) -> str:
    """Decrypt one Chromium v10 cookie value. Raises SessionError on failure."""
    if not encrypted_value:
        return ""
    if encrypted_value[:3] not in (b"v10", b"v11"):
        # Legacy plaintext
        try:
            return encrypted_value.decode("utf-8")
        except UnicodeDecodeError as exc:
            raise SessionError("cookie is not Chromium v10 and is not utf-8") from exc

    key = derive_chromium_key(password)
    payload = encrypted_value[3:]
    if len(payload) < 16 or len(payload) % 16:
        raise SessionError("truncated Chromium cookie ciphertext")
    plaintext = _unpad_pkcs7(AES.new(key, AES.MODE_CBC, b" " * 16).decrypt(payload))
    if db_version >= CHROMIUM_HASH_PREFIX_VERSION and len(plaintext) >= 32:
        expected = hashlib.sha256(host_key.encode("utf-8")).digest()
        if plaintext[:32] == expected:
            plaintext = plaintext[32:]
        # If the prefix does not match, keep bytes: wrong password yields garbage either way.
    try:
        text = plaintext.decode("utf-8")
    except UnicodeDecodeError as exc:
        raise SessionError("cookie decrypted to non-utf8 (wrong desktop key)") from exc
    if not text.isprintable():
        raise SessionError("cookie decrypted to non-printable data (wrong desktop key)")
    return text


def _keychain_password(service: str, account: str) -> str | None:
    try:
        proc = subprocess.run(
            ["security", "find-generic-password", "-s", service, "-a", account, "-w"],
            check=False,
            capture_output=True,
            text=True,
            timeout=10,
        )
    except (OSError, subprocess.TimeoutExpired):
        return None
    if proc.returncode != 0:
        return None
    password = proc.stdout.strip("\n")
    return password or None


def candidate_cookie_passwords() -> list[tuple[str, str]]:
    """(source, password) pairs to try against the Chromium cookie DB.

    Lark.app stores the OSCrypt password in Suite App Safe Storage. Tests
    inject LARKDESK_COOKIE_PASSWORD. Keychain is tried unless
    LARKDESK_TRY_KEYCHAIN=0.
    """
    env = os.environ.get("LARKDESK_COOKIE_PASSWORD")
    if env:
        return [("env", env)]
    if os.environ.get("LARKDESK_TRY_KEYCHAIN") == "0":
        return []
    found: list[tuple[str, str]] = []
    pairs = (
        ("Chrome Safe Storage", "Chrome"),
        ("Chromium Safe Storage", "Chromium"),
        ("Suite App Safe Storage", "XY6NLV7YTS.com.electron.lark-SuiteApp"),
        ("Suite App Safe Storage", "XY6NLV7YTS.com.electron.lark.iron-SuiteApp"),
    )
    for service, account in pairs:
        password = _keychain_password(service, account)
        if password:
            found.append((f"keychain:{service}:{account}", password))
    return found


def load_session_cookies(
    cookies_path: Path, passwords: Iterable[tuple[str, str]] | None = None
) -> tuple[dict[str, str], str]:
    """Return decrypted session cookies and the password source that worked."""
    if not cookies_path.is_file():
        raise SessionError(f"missing desktop cookie store: {cookies_path}")

    con = _open_ro(cookies_path)
    try:
        db_version = chromium_db_version(cookies_path)
        rows = con.execute(
            "SELECT host_key, name, value, encrypted_value FROM cookies"
        ).fetchall()
    except sqlite3.Error as exc:
        raise SessionError(f"cannot read desktop cookie store: {exc}") from exc
    finally:
        con.close()

    session_rows = [
        (host, name, value or "", encrypted or b"")
        for host, name, value, encrypted in rows
        if name in SESSION_COOKIE_NAMES
    ]
    if not session_rows:
        raise SessionError("desktop cookie store has no session cookies; log into Lark.app")

    # Plaintext values (rare) are used as-is.
    plaintext_cookies: dict[str, str] = {}
    for host, name, value, encrypted in session_rows:
        if value:
            plaintext_cookies[name] = value

    if plaintext_cookies.get("session") or plaintext_cookies.get("osession"):
        return plaintext_cookies, "plaintext"

    attempts = list(passwords) if passwords is not None else candidate_cookie_passwords()
    last_error: Exception | None = None
    for source, password in attempts:
        decoded: dict[str, str] = dict(plaintext_cookies)
        try:
            for host, name, _value, encrypted in session_rows:
                if not encrypted:
                    continue
                decoded[name] = decrypt_chromium_value(
                    encrypted, password, host_key=host, db_version=db_version
                )
            if decoded.get("session") or decoded.get("osession") or decoded.get("sl_session"):
                return decoded, source
        except SessionError as exc:
            last_error = exc
            continue
    if last_error:
        raise SessionError(
            "desktop session cookies are present but could not be decrypted "
            f"({last_error})"
        )
    raise SessionError("desktop session cookies could not be decrypted")


def cookies_path_for_user(larkshell: Path, user_id: str) -> Path:
    digest = user_dir_hash(user_id)
    return larkshell / "aha" / "users" / digest / "profile_explorer" / "Cookies"


def session_cookie_rows_exist(cookies_path: Path) -> bool:
    if not cookies_path.is_file():
        return False
    try:
        con = _open_ro(cookies_path)
        try:
            row = con.execute(
                "SELECT 1 FROM cookies WHERE name IN ('session','osession','sl_session') LIMIT 1"
            ).fetchone()
            return row is not None
        finally:
            con.close()
    except sqlite3.Error:
        return False


def load_session(
    larkshell: Path | None = None,
    *,
    cookie_passwords: Iterable[tuple[str, str]] | None = None,
    require_decrypted_cookies: bool = False,
) -> AuthMaterial:
    """Load identity from the desktop session.

    Decrypted cookies are attached when a working Chromium cookie password is
    available. Identity still loads if cookies stay encrypted, as long as the
    Chromium session rows exist — that is the logged-in desktop session.
    """
    root = larkshell or default_larkshell()
    if not root.is_dir():
        raise SessionError(f"Lark desktop data directory not found: {root}")

    identity = load_identity(root)
    cookies_path = cookies_path_for_user(root, identity.user_id)
    if not session_cookie_rows_exist(cookies_path):
        raise SessionError(
            "no Chromium session cookies for this user; log into Lark.app first"
        )

    cookies: dict[str, str] = {}
    source = ""
    try:
        cookies, source = load_session_cookies(cookies_path, cookie_passwords)
    except SessionError:
        if require_decrypted_cookies:
            raise
        source = "encrypted-present"
    return AuthMaterial(identity=identity, cookies=cookies, cookie_password_source=source)
