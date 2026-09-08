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

func TestEncodePutImage(t *testing.T) {
	raw := EncodePutImage("123", "img_v3_abc")
	fields := map[int]pb.Field{}
	for _, f := range pb.DecodeFields(raw) {
		fields[f.Num] = f
	}
	if fields[1].Int != MsgTypeImage {
		t.Fatalf("type %d", fields[1].Int)
	}
	if string(fields[3].Bytes) != "123" {
		t.Fatal(string(fields[3].Bytes))
	}
	if !bytes.Contains(raw, []byte("img_v3_abc")) {
		t.Fatal("missing key")
	}
	content := fields[2].Bytes
	inner := map[int]pb.Field{}
	for _, f := range pb.DecodeFields(content) {
		inner[f.Num] = f
	}
	if inner[2].Wire != "bytes" {
		t.Fatalf("want imageV2, got %+v", inner)
	}
}

func TestEncodePutFile(t *testing.T) {
	raw := EncodePutFile("123", "img_v3_abc", "a.png", "image/png", 67)
	fields := map[int]pb.Field{}
	for _, f := range pb.DecodeFields(raw) {
		fields[f.Num] = f
	}
	if fields[1].Int != MsgTypeFile {
		t.Fatalf("type %d", fields[1].Int)
	}
	inner := map[int]pb.Field{}
	for _, f := range pb.DecodeFields(fields[2].Bytes) {
		inner[f.Num] = f
	}
	if string(inner[6].Bytes) != "img_v3_abc" || string(inner[11].Bytes) != "a.png" {
		t.Fatalf("%+v", inner)
	}
}

func TestMimeForPath(t *testing.T) {
	if mimeForPath("x.PNG") != "image/png" {
		t.Fatal(mimeForPath("x.PNG"))
	}
}
