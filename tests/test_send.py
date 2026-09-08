"""PUT_MESSAGE payload encoding."""

from __future__ import annotations

import unittest

from larkdesk.gateway import decode_fields
from larkdesk.send import MSG_TYPE_TEXT, encode_put_message, encode_rich_text, parse_put_message_response
from larkdesk.gateway import encode_bytes, encode_string


class SendCodecTests(unittest.TestCase):
    def test_rich_text_has_inner(self) -> None:
        raw = encode_rich_text("hello")
        self.assertIn(b"hello", raw)

    def test_put_message_fields(self) -> None:
        raw = encode_put_message("123", "hello")
        fields = {f: (k, v) for f, k, v in decode_fields(raw)}
        self.assertEqual(fields[1], ("varint", MSG_TYPE_TEXT))
        self.assertEqual(fields[3][0], "bytes")
        self.assertEqual(bytes(fields[3][1]).decode(), "123")
        self.assertIn(b"hello", bytes(fields[2][1]))

    def test_parse_response_id(self) -> None:
        inner = encode_string(1, "999")
        payload = encode_bytes(1, inner)
        self.assertEqual(parse_put_message_response(payload), "999")


if __name__ == "__main__":
    unittest.main()
