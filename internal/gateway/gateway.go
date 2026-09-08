package gateway

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/TensorFu/larkdesk/internal/httpx"
	"github.com/TensorFu/larkdesk/internal/pb"
	"github.com/TensorFu/larkdesk/internal/session"
)

const GatewayURL = "https://internal-api-lark-api.feishu.cn/im/gateway/"

func PostPacket(auth session.Auth, cmd uint64, payload []byte) (pb.Packet, error) {
	header := auth.CookieHeader()
	if !strings.Contains(header, "session=") && !strings.Contains(header, "osession=") && !strings.Contains(header, "sl_session=") {
		return pb.Packet{}, fmt.Errorf("no decrypted session cookie for gateway")
	}
	body, cid := pb.EncodePacket(cmd, payload, "")
	req, err := http.NewRequest(http.MethodPost, GatewayURL, bytes.NewReader(body))
	if err != nil {
		return pb.Packet{}, err
	}
	req.Header.Set("User-Agent", "larkdesk/0.1")
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("X-Command", strconv.FormatUint(cmd, 10))
	req.Header.Set("X-Request-Id", cid)
	req.Header.Set("X-Source", "web")
	req.Header.Set("Origin", "https://www.feishu.cn")
	req.Header.Set("Referer", "https://www.feishu.cn/messenger/")
	req.Header.Set("Cookie", header)
	resp, err := httpx.Client(20 * time.Second).Do(req)
	if err != nil {
		return pb.Packet{}, fmt.Errorf("gateway %v cmd=%d", err, cmd)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return pb.Packet{}, err
	}
	if resp.StatusCode >= 400 {
		return pb.Packet{}, fmt.Errorf("gateway HTTP %d cmd=%d", resp.StatusCode, cmd)
	}
	return pb.DecodePacket(raw), nil
}

func PostCommand(auth session.Auth, cmd uint64, payload []byte) ([]byte, error) {
	pkt, err := PostPacket(auth, cmd, payload)
	if err != nil {
		return nil, err
	}
	return pkt.Payload, nil
}
