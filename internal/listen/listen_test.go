package listen

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/TensorFu/larkdesk/internal/frontier"
	"github.com/TensorFu/larkdesk/internal/pb"
	"github.com/TensorFu/larkdesk/internal/send"
)

const capturedProbeHex = "089de09d3a10f2bdbd83ebadd4e918180120012a280a18785f66726f6e746965725f72656369657665645f74696d65120c646c3976306a346c317676672a160a123a785f66726f6e746965725f6d73675f696412002a220a0b582d417574682d557365721213373134343933343730353531383035313332392a290a193a785f66726f6e746965725f6d73675f73656e645f74696d65120c646c3976306a346c64757a312a460a0b7472616365706172656e74123730302d64323636616161613638633663616333393131376166616163636339646336652d626261363331383437613232326461312d303132003a004292040a1337363833313038313233363934393433323139100118062af4030ae3020a133736383331303831323239363039303732353112cb020a133736383331303831323239363039303732353110041a133731343439333437303535313830353133323920a1c4ffd4062a280a001a240a013312001a1b0a190a0133121408011a100a0e6c61726b6465736b2d70726f6265680130013801521337363030"

func message(mid, fromID, chatID, text string, ts uint64) []byte {
	content := pb.EncodeBytes(1, send.EncodeRichText(text))
	out := pb.EncodeString(1, mid)
	out = append(out, pb.EncodeInt(2, send.MsgTypeText)...)
	out = append(out, pb.EncodeString(3, fromID)...)
	out = append(out, pb.EncodeInt(4, ts)...)
	out = append(out, pb.EncodeBytes(5, content)...)
	out = append(out, pb.EncodeString(10, chatID)...)
	return out
}

func pushPayload(msg []byte, mid string) []byte {
	item := append(pb.EncodeString(1, mid), pb.EncodeBytes(2, msg)...)
	return pb.EncodeBytes(1, item)
}

func packet(cmd uint64, payload []byte) []byte {
	out := pb.EncodeInt(2, 1)
	out = append(out, pb.EncodeInt(3, cmd)...)
	out = append(out, pb.EncodeBytes(5, payload)...)
	return out
}

func frame(pkt []byte) []byte {
	header := pb.EncodeBytes(5, append(pb.EncodeString(1, "X-Auth-User"), pb.EncodeString(2, "1")...))
	out := pb.EncodeInt(3, 1)
	out = append(out, pb.EncodeInt(4, 1)...)
	out = append(out, header...)
	out = append(out, pb.EncodeBytes(8, pkt)...)
	return out
}

func TestAccessKey(t *testing.T) {
	a := frontier.AccessKey("7569243424336838657")
	if len(a) != 32 {
		t.Fatal(a)
	}
	if a != frontier.AccessKey("7569243424336838657") {
		t.Fatal("not stable")
	}
}

func TestExtractRichText(t *testing.T) {
	raw := pb.EncodeBytes(1, send.EncodeRichText("hello 钟林"))
	if ExtractPlainText(raw) != "hello 钟林" {
		t.Fatalf("%q", ExtractPlainText(raw))
	}
}

func TestExtractCaptured(t *testing.T) {
	content, _ := hex.DecodeString("0a001a240a013312001a1b0a190a0133121408011a100a0e6c61726b6465736b2d70726f62656801")
	got := ExtractPlainText(content)
	if !strings.Contains(got, "larkdesk-probe") {
		t.Fatalf("%q", got)
	}
}

func TestCapturedHeaders(t *testing.T) {
	raw, _ := hex.DecodeString(capturedProbeHex)
	h := frontier.DecodeHeaders(raw)
	if h["X-Auth-User"] != "7144934705518051329" {
		t.Fatalf("%v", h)
	}
}

func TestParsePushAndEvent(t *testing.T) {
	mid, fromID, chatID := "111", "222", "333"
	msg := message(mid, fromID, chatID, "ping", 1700000000)
	payload := pushPayload(msg, mid)
	parsed := ParsePushMessages(payload)
	if len(parsed) != 1 || parsed[0].ID != mid || parsed[0].FromID != fromID || parsed[0].ChatID != chatID {
		t.Fatalf("%+v", parsed)
	}
	if ExtractPlainText(parsed[0].Content) != "ping" {
		t.Fatalf("%q", ExtractPlainText(parsed[0].Content))
	}
	fr := frame(packet(CmdPushMessages, payload))
	events := EventsFromFrame(fr, "222", map[string]string{fromID: "Ada"})
	if len(events) != 1 {
		t.Fatalf("%+v", events)
	}
	ev := events[0]
	if ev.Text != "ping" || ev.FromID != fromID || ev.FromName != "Ada" || ev.ChatID != chatID || !ev.Self {
		t.Fatalf("%+v", ev)
	}
}

func TestSkipsNonPush(t *testing.T) {
	if ev := EventsFromFrame(frame(packet(1, nil)), "", nil); len(ev) != 0 {
		t.Fatal(ev)
	}
}
