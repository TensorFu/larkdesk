"""larkdesk command-line entry."""

from __future__ import annotations

import argparse
import json
import sys
from typing import Sequence

from larkdesk import __version__
from larkdesk.contacts import ContactsError, list_contacts
from larkdesk.listen import ListenError, run_listen
from larkdesk.search import SearchError, search_contacts
from larkdesk.send import SendError, send_text
from larkdesk.session import SessionError, load_session


def _print_json(payload: object) -> None:
    json.dump(payload, sys.stdout, ensure_ascii=False, indent=2)
    sys.stdout.write("\n")


def cmd_whoami(_args: argparse.Namespace) -> int:
    try:
        auth = load_session()
    except SessionError as exc:
        print(f"larkdesk: {exc}", file=sys.stderr)
        return 1
    ident = auth.identity
    _print_json(
        {
            "ok": True,
            "name": ident.name,
            "id": ident.user_id,
            "tenant": ident.tenant_name,
            "tenant_id": ident.tenant_id,
            "domain": ident.domain,
            "auth": "desktop-session",
        }
    )
    return 0


def cmd_contacts(_args: argparse.Namespace) -> int:
    try:
        auth, contacts, source = list_contacts()
    except (SessionError, ContactsError) as exc:
        print(f"larkdesk: {exc}", file=sys.stderr)
        return 1
    _print_json(
        {
            "ok": True,
            "identity": {
                "name": auth.identity.name,
                "id": auth.identity.user_id,
            },
            "contacts": [c.as_dict() for c in contacts],
            "count": len(contacts),
            "source": source,
        }
    )
    return 0


def cmd_search(args: argparse.Namespace) -> int:
    try:
        auth, hits = search_contacts(args.query)
    except (SessionError, SearchError) as exc:
        print(f"larkdesk: {exc}", file=sys.stderr)
        return 1
    _print_json(
        {
            "ok": True,
            "query": args.query,
            "identity": {
                "name": auth.identity.name,
                "id": auth.identity.user_id,
            },
            "contacts": [c.as_dict() for c in hits],
            "count": len(hits),
            "source": "universal_search",
        }
    )
    return 0


def cmd_send(args: argparse.Namespace) -> int:
    text = " ".join(args.text).strip()
    try:
        result = send_text(args.to, text, dry_run=args.dry_run)
    except (SessionError, SearchError, SendError) as exc:
        print(f"larkdesk: {exc}", file=sys.stderr)
        return 1
    _print_json({"ok": True, **result, "source": "put_p2p+put_message"})
    return 0


def cmd_listen(args: argparse.Namespace) -> int:
    try:
        run_listen(from_who=args.from_who, exec_cmd=args.exec_cmd)
    except KeyboardInterrupt:
        return 0
    except (SessionError, SearchError, SendError, ListenError) as exc:
        print(f"larkdesk: {exc}", file=sys.stderr)
        return 1
    return 0


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="larkdesk",
        description=(
            "Reuse the already-logged-in local Lark.app / 飞书 desktop session. "
            "Does not run lark-cli or start a new OAuth login."
        ),
    )
    parser.add_argument("--version", action="version", version=f"larkdesk {__version__}")
    sub = parser.add_subparsers(dest="command", required=True)
    who = sub.add_parser("whoami", help="show the desktop-session user identity")
    who.set_defaults(func=cmd_whoami)
    contacts = sub.add_parser("contacts", help="list 联系人 (name + stable user id)")
    contacts.set_defaults(func=cmd_contacts)
    search = sub.add_parser("search", help="search 联系人 by name")
    search.add_argument("query", help="search query")
    search.set_defaults(func=cmd_search)
    sendp = sub.add_parser("send", help="send text to a person (name or id)")
    sendp.add_argument("to", help="person name or user id")
    sendp.add_argument("text", nargs="+", help="message text")
    sendp.add_argument("--dry-run", action="store_true", help="resolve chat only, do not send")
    sendp.set_defaults(func=cmd_send)
    listenp = sub.add_parser("listen", help="stream inbound text messages as JSONL")
    listenp.add_argument("--from", dest="from_who", default="", help="only this person (name or id)")
    listenp.add_argument("--exec", dest="exec_cmd", default="", help="run command with each event JSON on stdin")
    listenp.set_defaults(func=cmd_listen)
    return parser


def main(argv: Sequence[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    return int(args.func(args))


if __name__ == "__main__":
    sys.exit(main())
