package frontier

import (
	"bytes"
	"compress/gzip"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/TensorFu/larkdesk/internal/httpx"
	"github.com/TensorFu/larkdesk/internal/pb"
	"github.com/TensorFu/larkdesk/internal/session"
	"github.com/gorilla/websocket"
)

const (
	AppKeySaas     = "5f45da0e6c7a17dcba80494ef0ab9b21"
	AccessKeySalt  = "f8a69f1719916z"
	PassportTicket = "https://passport.feishu.cn/suite/passport/frontier_ticket/"
	FrontierHost   = "msg-frontier.feishu.cn"
)

func MaybeGunzip(data []byte) []byte {
	if len(data) < 2 || data[0] != 0x1f || data[1] != 0x8b {
		return data
	}
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return data
	}
	defer r.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		return data
	}
	return out
}

func AccessKey(deviceID string) string {
	sum := md5.Sum([]byte("2" + AppKeySaas + deviceID + AccessKeySalt))
	return hex.EncodeToString(sum[:])
}

func FetchTicket(auth session.Auth) (deviceID, ticket string, err error) {
	if !auth.HasSessionCookie() {
		return "", "", fmt.Errorf("no decrypted session cookie for frontier")
	}
	u := PassportTicket + "?" + url.Values{"local_device_id": {""}}.Encode()
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", "larkdesk/0.1")
	req.Header.Set("Cookie", auth.CookieHeader())
	req.Header.Set("Referer", "https://www.feishu.cn/messenger/")
	req.Header.Set("Origin", "https://www.feishu.cn")
	resp, err := httpx.Client(20 * time.Second).Do(req)
	if err != nil {
		return "", "", fmt.Errorf("frontier ticket %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", err
	}
	if resp.StatusCode >= 400 {
		return "", "", fmt.Errorf("frontier ticket HTTP %d", resp.StatusCode)
	}
	var body map[string]any
	if json.Unmarshal(raw, &body) != nil {
		return "", "", fmt.Errorf("frontier ticket was not JSON")
	}
	deviceID = fmt.Sprint(first(body, "device_id", "deviceId"))
	if deviceID == "<nil>" {
		deviceID = ""
	}
	ticket = fmt.Sprint(first(body, "ticket"))
	if ticket == "<nil>" {
		ticket = ""
	}
	if ticket == "" {
		return "", "", fmt.Errorf("frontier ticket missing")
	}
	if deviceID == "" {
		deviceID = "0"
	}
	return deviceID, ticket, nil
}

func first(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			return v
		}
	}
	return ""
}

func BuildWSURL(deviceID, ticket string) string {
	q := url.Values{
		"access_key":      {AccessKey(deviceID)},
		"aid":             {"1"},
		"ticket":          {ticket},
		"device_id":       {deviceID},
		"fpid":            {"2"},
		"accept_encoding": {"gzip"},
		"request_id":      {pb.NewUUID()},
	}
	return "wss://" + FrontierHost + "/ws/v2?" + q.Encode()
}

func DecodeHeaders(raw []byte) map[string]string {
	headers := map[string]string{}
	for _, f := range pb.DecodeFields(raw) {
		if f.Num != 5 || f.Wire != "bytes" {
			continue
		}
		name, val := "", ""
		for _, inner := range pb.DecodeFields(f.Bytes) {
			if inner.Wire != "bytes" {
				continue
			}
			if inner.Num == 1 {
				name = string(inner.Bytes)
			} else if inner.Num == 2 {
				val = string(inner.Bytes)
			}
		}
		if name != "" {
			headers[name] = val
		}
	}
	return headers
}

func DecodeBody(raw []byte) []byte {
	var body []byte
	for _, f := range pb.DecodeFields(raw) {
		if f.Num == 8 && f.Wire == "bytes" {
			body = f.Bytes
		}
	}
	return MaybeGunzip(body)
}

type Frame struct {
	Headers map[string]string
	pb.Packet
}

func DecodeFrame(raw []byte) Frame {
	raw = MaybeGunzip(raw)
	headers := DecodeHeaders(raw)
	body := DecodeBody(raw)
	out := Frame{Headers: headers}
	if len(body) > 0 {
		enc := headers["content-encoding"]
		if enc == "" {
			enc = headers["Content-Encoding"]
		}
		if enc == "gzip" {
			body = MaybeGunzip(body)
		}
		out.Packet = pb.DecodePacket(body)
	}
	return out
}

func encodeHeader(k, v string) []byte {
	return pb.EncodeBytes(5, append(pb.EncodeString(1, k), pb.EncodeString(2, v)...))
}

func EncodeFrame(seq, ts uint64, packet []byte, userID string, cmd uint64) []byte {
	var b []byte
	b = append(b, pb.EncodeInt(1, seq)...)
	b = append(b, pb.EncodeInt(2, ts)...)
	b = append(b, pb.EncodeInt(3, 1)...)
	b = append(b, pb.EncodeInt(4, 1)...)
	cmdStr := fmt.Sprintf("%d", cmd)
	for _, kv := range [][2]string{
		{"content-type", "application/x-protobuf"},
		{"x-command", cmdStr},
		{"x-command-version", "2.7.0"},
		{"x-Source", "web"},
		{"X-Auth-User", userID},
		{"To-Cluster", "im"},
	} {
		b = append(b, encodeHeader(kv[0], kv[1])...)
	}
	b = append(b, pb.EncodeBytes(6, nil)...)
	b = append(b, pb.EncodeBytes(7, nil)...)
	b = append(b, pb.EncodeBytes(8, packet)...)
	return b
}

func Call(auth session.Auth, cmd uint64, payload []byte, echo []byte) ([]byte, error) {
	conn, err := Dial(auth)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	packet, _ := pb.EncodePacket(cmd, payload, "")
	frame := EncodeFrame(1, uint64(time.Now().UnixNano()), packet, auth.Identity.UserID, cmd)
	if err := conn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
		return nil, err
	}
	_ = conn.SetReadDeadline(time.Now().Add(8 * time.Second))
	for i := 0; i < 16; i++ {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return nil, err
		}
		raw = MaybeGunzip(raw)
		if echo != nil && bytes.Contains(raw, echo) {
			return raw, nil
		}
		fr := DecodeFrame(raw)
		if fr.Cmd == cmd && len(fr.Payload) > 0 {
			return raw, nil
		}
	}
	return nil, fmt.Errorf("frontier call timeout cmd=%d", cmd)
}

func Dial(auth session.Auth) (*websocket.Conn, error) {
	deviceID, ticket, err := FetchTicket(auth)
	if err != nil {
		return nil, err
	}
	u := BuildWSURL(deviceID, ticket)
	d := websocket.Dialer{
		Proxy:             http.ProxyURL(httpx.ProxyURL()),
		HandshakeTimeout:  20 * time.Second,
		EnableCompression: true,
	}
	h := http.Header{}
	h.Set("Origin", "https://www.feishu.cn")
	h.Set("Cookie", auth.CookieHeader())
	h.Set("User-Agent", "larkdesk/0.1")
	conn, resp, err := d.Dial(u, h)
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(8 << 20)
	return conn, nil
}
