package search

import (
	"testing"

	"github.com/TensorFu/larkdesk/internal/pb"
)

func TestEncodeRequest(t *testing.T) {
	raw, err := EncodeRequest("钟林", 20)
	if err != nil {
		t.Fatal(err)
	}
	top := pb.DecodeFields(raw)
	if top[0].Num != 1 {
		t.Fatal(top)
	}
	header := pb.DecodeFields(top[0].Bytes)
	by := map[int]pb.Field{}
	for _, f := range header {
		by[f.Num] = f
	}
	if string(by[3].Bytes) != "钟林" {
		t.Fatalf("%q", by[3].Bytes)
	}
}

func TestParseStripsHighlight(t *testing.T) {
	result := pb.EncodeString(1, "7075164837923258372")
	result = append(result, pb.EncodeInt(2, 1)...)
	result = append(result, pb.EncodeString(3, "<h>钟林</h>")...)
	payload := pb.EncodeBytes(2, result)
	hits := ParseResponse(payload)
	if len(hits) != 1 || hits[0].Name != "钟林" || hits[0].ID != "7075164837923258372" {
		t.Fatalf("%+v", hits)
	}
}

func TestEmptyQuery(t *testing.T) {
	_, err := EncodeRequest("  ", 20)
	if err == nil {
		t.Fatal("expected error")
	}
}
