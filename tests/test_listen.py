"""Decode frontier PUSH_MESSAGES frames into inbound text events."""

from __future__ import annotations

import unittest

from larkdesk.cli import build_parser
from larkdesk.frontier import access_key, decode_frontier_headers, decode_frontier_packet
from larkdesk.gateway import encode_bytes, encode_int32, encode_string
from larkdesk.listen import (
    CMD_PUSH_MESSAGES,
    events_from_frame,
    extract_plain_text,
    parse_push_messages,
)
from larkdesk.send import MSG_TYPE_TEXT, encode_rich_text

# Truncated live capture; body is incomplete but headers + plaintext remain.
CAPTURED_PROBE_HEX = (
    "089de09d3a10f2bdbd83ebadd4e918180120012a280a18785f66726f6e746965725f726563696576"
    "65645f74696d65120c646c3976306a346c317676672a160a123a785f66726f6e746965725f6d7367"
    "5f696412002a220a0b582d417574682d5573657212133731343439333437303535313830353133"
    "32392a290a193a785f66726f6e746965725f6d73675f73656e645f74696d65120c646c3976306a34"
    "6c64757a312a460a0b7472616365706172656e74123730302d6432363661616161363863366361"
    "6333393131376166616163636339646336652d626261363331383437613232326461312d303132"
    "003a004292040a1337363833313038313233363934393433323139100118062af4030ae3020a13"
    "3736383331303831323239363039303732353112cb020a13373638333130383132323936303930"
    "3732353110041a133731343439333437303535313830353133323920a1c4ffd4062a280a001a24"
    "0a013312001a1b0a190a0133121408011a100a0e6c61726b6465736b2d70726f62656801300138"
    "01521337363030"
)


def _message(mid: str, from_id: str, chat_id: str, text: str, ts: int = 1700000000) -> bytes:
    content = encode_bytes(1, encode_rich_text(text))
    return (
        encode_string(1, mid)
        + encode_int32(2, MSG_TYPE_TEXT)
        + encode_string(3, from_id)
        + encode_int32(4, ts)
        + encode_bytes(5, content)
        + encode_string(10, chat_id)
    )


def _push_payload(message: bytes, mid: str) -> bytes:
    item = encode_string(1, mid) + encode_bytes(2, message)
    return encode_bytes(1, item)


def _packet(cmd: int, payload: bytes) -> bytes:
    return encode_int32(2, 1) + encode_int32(3, cmd) + encode_bytes(5, payload)


def _frame(packet: bytes) -> bytes:
    header = encode_bytes(5, encode_string(1, "X-Auth-User") + encode_string(2, "1"))
    return encode_int32(3, 1) + encode_int32(4, 1) + header + encode_bytes(8, packet)


class ListenCodecTests(unittest.TestCase):
    def test_access_key_md5(self) -> None:
        key = access_key("7569243424336838657")
        self.assertEqual(len(key), 32)
        self.assertEqual(key, access_key("7569243424336838657"))

    def test_extract_rich_text(self) -> None:
        raw = encode_bytes(1, encode_rich_text("hello 钟林"))
        self.assertEqual(extract_plain_text(raw), "hello 钟林")

    def test_extract_from_captured_bytes(self) -> None:
        content = bytes.fromhex(
            "0a001a240a013312001a1b0a190a0133121408011a100a0e6c61726b6465736b2d70726f62656801"
        )
        self.assertIn("larkdesk-probe", extract_plain_text(content))

    def test_captured_headers(self) -> None:
        headers = decode_frontier_headers(bytes.fromhex(CAPTURED_PROBE_HEX))
        self.assertEqual(headers.get("X-Auth-User"), "7144934705518051329")

    def test_parse_push_and_event(self) -> None:
        mid = "111"
        from_id = "222"
        chat_id = "333"
        msg = _message(mid, from_id, chat_id, "ping")
        payload = _push_payload(msg, mid)
        parsed = parse_push_messages(payload)
        self.assertEqual(len(parsed), 1)
        self.assertEqual(parsed[0]["id"], mid)
        self.assertEqual(parsed[0]["from_id"], from_id)
        self.assertEqual(parsed[0]["chat_id"], chat_id)
        self.assertEqual(extract_plain_text(parsed[0]["content"]), "ping")  # type: ignore[arg-type]

        frame = _frame(_packet(CMD_PUSH_MESSAGES, payload))
        pkt = decode_frontier_packet(frame)
        self.assertEqual(pkt.get("cmd"), CMD_PUSH_MESSAGES)
        events = events_from_frame(frame, self_id="222", names={from_id: "Ada"})
        self.assertEqual(len(events), 1)
        ev = events[0]
        self.assertEqual(ev.text, "ping")
        self.assertEqual(ev.from_id, from_id)
        self.assertEqual(ev.from_name, "Ada")
        self.assertEqual(ev.chat_id, chat_id)
        self.assertTrue(ev.self)
        self.assertEqual(ev.as_dict()["from"]["name"], "Ada")

    def test_skips_non_push(self) -> None:
        frame = _frame(_packet(1, b""))
        self.assertEqual(events_from_frame(frame), [])

    def test_cli_has_listen(self) -> None:
        parser = build_parser()
        args = parser.parse_args(["listen", "--from", "钟林", "--exec", "./hook.sh"])
        self.assertEqual(args.from_who, "钟林")
        self.assertEqual(args.exec_cmd, "./hook.sh")


if __name__ == "__main__":
    unittest.main()
