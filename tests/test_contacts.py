"""Drive the shipped contact parser and list_contacts on fixtures."""

from __future__ import annotations

import json
import sqlite3
import tempfile
import unittest
from pathlib import Path

from larkdesk.contacts import list_contacts, parse_contact_payload, parse_pull_contacts_v2
from larkdesk.gateway import decode_packet, encode_bytes, encode_int32, encode_packet, encode_string

from test_session import (
    FIXTURE_NAME,
    FIXTURE_PASSWORD,
    FIXTURE_USER_ID,
    _fixture_tree,
)

ORG_PAYLOAD = {
    "orgVal": {
        "root": {
            "departmentId": "dept-1",
            "dataSource": [
                {
                    "id": "10001",
                    "type": 0,
                    "entity": {
                        "id": "10001",
                        "name": "Fixture User",
                        "localizedName": "Fixture User",
                        "avatarKey": "av-1",
                        "departmentName": "Engineering",
                    },
                },
                {
                    "id": "10002",
                    "type": 0,
                    "entity": {
                        "id": "10002",
                        "name": "Colleague One",
                        "localizedName": "Colleague One",
                        "avatarKey": "av-2",
                        "departmentName": "Engineering",
                    },
                },
            ],
            "department": {
                "id": "dept-1",
                "name": "Engineering",
                "parentId": "dept-0",
                "leaderId": "10009",
            },
        }
    }
}

GETCONTACTS_PAYLOAD = {
    "data": {
        "users": [
            {
                "open_id": "ou_aaa",
                "localized_name": "Alice",
                "department": "Ops",
            },
            {
                "open_id": "ou_bbb",
                "localized_name": "Bob",
            },
        ]
    }
}


class ParsePayloadTests(unittest.TestCase):
    def test_org_cache_payload(self) -> None:
        contacts = parse_contact_payload(ORG_PAYLOAD)
        by_id = {c.id: c.name for c in contacts}
        self.assertEqual(by_id.get("10001"), "Fixture User")
        self.assertEqual(by_id.get("10002"), "Colleague One")
        self.assertNotIn("dept-1", by_id)

    def test_getcontacts_like_payload(self) -> None:
        contacts = parse_contact_payload(GETCONTACTS_PAYLOAD)
        self.assertEqual(
            [(c.name, c.id) for c in contacts],
            [("Alice", "ou_aaa"), ("Bob", "ou_bbb")],
        )

    def test_empty_payload_is_empty_not_fake(self) -> None:
        self.assertEqual(parse_contact_payload({}), [])
        self.assertEqual(parse_contact_payload({"data": {"users": []}}), [])

    def test_pull_contacts_v2_protobuf(self) -> None:
        profile = encode_string(2, "Alice") + encode_string(6, "ou_aaa") + encode_string(4, "Ops")
        entry = encode_bytes(1, profile)
        payload = (
            encode_int32(1, 0)
            + encode_int32(2, 300)
            + encode_int32(3, 1)
            + encode_bytes(4, entry)
        )
        contacts = parse_pull_contacts_v2(payload)
        self.assertEqual([(c.name, c.id) for c in contacts], [("Alice", "ou_aaa")])
        self.assertEqual(contacts[0].extra, {"tenant": "Ops"})

    def test_packet_roundtrip_cmd(self) -> None:
        body, cid = encode_packet(1100310, b"")
        pkt = decode_packet(body)
        self.assertEqual(pkt["cmd"], 1100310)
        self.assertEqual(pkt["payload_type"], 1)
        self.assertEqual(pkt["cid"], cid)


def _contacts_tree() -> Path:
    root = _fixture_tree()
    con = sqlite3.connect(root / "persistent_storage.db")
    con.execute(
        "INSERT INTO kv_default VALUES (?,?,?,?,?)",
        (
            "23",
            "LARKW_ORGANIZATION_ROOT",
            json.dumps(ORG_PAYLOAD, ensure_ascii=False),
            "default",
            "0",
        ),
    )
    con.commit()
    con.close()
    return root


class ListContactsTests(unittest.TestCase):
    def test_list_contacts_from_desktop_fixture(self) -> None:
        root = _contacts_tree()
        auth, contacts, source = list_contacts(
            root, cookie_passwords=[("fixture", FIXTURE_PASSWORD)]
        )
        self.assertEqual(source, "desktop-session")
        self.assertEqual(auth.identity.user_id, FIXTURE_USER_ID)
        by_id = {c.id: c.name for c in contacts}
        self.assertEqual(by_id[FIXTURE_USER_ID], FIXTURE_NAME)
        self.assertEqual(by_id["10002"], "Colleague One")
        self.assertGreaterEqual(len(contacts), 2)

    def test_missing_session_does_not_return_empty_success(self) -> None:
        root = Path(tempfile.mkdtemp(prefix="larkdesk-nocontacts-"))
        with self.assertRaises(Exception):
            list_contacts(root)


if __name__ == "__main__":
    unittest.main()
