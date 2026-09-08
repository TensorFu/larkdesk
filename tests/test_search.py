"""Universal search request/response parsing."""

from __future__ import annotations

import unittest

from larkdesk.gateway import decode_fields, encode_bytes, encode_int32, encode_string
from larkdesk.search import (
    CMD_UNIVERSAL_SEARCH,
    encode_universal_search_request,
    parse_universal_search_response,
)


class SearchCodecTests(unittest.TestCase):
    def test_request_has_query_and_user_entity(self) -> None:
        raw = encode_universal_search_request("钟林")
        top = decode_fields(raw)
        self.assertEqual(top[0][0], 1)
        header = decode_fields(top[0][2])  # type: ignore[arg-type]
        by = {f: v for f, k, v in header}
        self.assertEqual(bytes(by[3]).decode("utf-8"), "钟林")
        ctx = decode_fields(by[5])  # type: ignore[arg-type]
        items = [v for f, k, v in ctx if f == 2]
        self.assertTrue(items)
        entity = decode_fields(items[0])  # type: ignore[arg-type]
        self.assertEqual(entity[0], (1, "varint", 1))

    def test_parse_strips_highlight_and_keeps_user(self) -> None:
        result = (
            encode_string(1, "7075164837923258372")
            + encode_int32(2, 1)
            + encode_string(3, "<h>钟林</h>")
        )
        payload = encode_bytes(2, result)
        hits = parse_universal_search_response(payload)
        self.assertEqual([(c.name, c.id) for c in hits], [("钟林", "7075164837923258372")])

    def test_cmd_id(self) -> None:
        self.assertEqual(CMD_UNIVERSAL_SEARCH, 11021)


if __name__ == "__main__":
    unittest.main()
