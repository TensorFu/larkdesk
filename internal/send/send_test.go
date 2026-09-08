package send

import (
	"bytes"
	"testing"

	"github.com/TensorFu/larkdesk/internal/pb"
)

func TestRichTextHasInner(t *testing.T) {
	raw := EncodeRichText("hello")
	if !bytes.Contains(raw, []byte("hello")) {
		t.Fatal(raw)
	}
}

func TestPutMessageFields(t *testing.T) {
	raw := EncodePutMessage("123", "hello")
	fields := map[int]pb.Field{}
	for _, f := range pb.DecodeFields(raw) {
		fields[f.Num] = f
	}
	if fields[1].Int != MsgTypeText {
		t.Fatalf("type %d", fields[1].Int)
	}
	if string(fields[3].Bytes) != "123" {
		t.Fatalf("chat %q", fields[3].Bytes)
	}
	if !bytes.Contains(fields[2].Bytes, []byte("hello")) {
		t.Fatal("missing hello")
	}
}

func TestParseResponseID(t *testing.T) {
	inner := pb.EncodeString(1, "999")
	payload := pb.EncodeBytes(1, inner)
	if ParsePutMessageResponse(payload) != "999" {
		t.Fatal(ParsePutMessageResponse(payload))
	}
}
