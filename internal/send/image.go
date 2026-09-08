package send

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/TensorFu/larkdesk/internal/gateway"
	"github.com/TensorFu/larkdesk/internal/httpx"
	"github.com/TensorFu/larkdesk/internal/pb"
	"github.com/TensorFu/larkdesk/internal/session"
)

const (
	MsgTypeFile      = 2
	MsgTypeImage     = 5
	uploadURL        = "https://internal-api-lark-file.feishu.cn/uni_api/upload/file"
	uploadFileType   = 2
	uploadMountPoint = "im_file"
)

func mimeForPath(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	}
	if t := mime.TypeByExtension(ext); strings.HasPrefix(t, "image/") {
		return t
	}
	return "image/png"
}

func EncodePutImage(chatID, key string) []byte {
	content := pb.EncodeString(2, key)                     // imageKey
	content = append(content, pb.EncodeString(10, key)...) // cryptoToken — ValidateKey reads this
	content = append(content, pb.EncodeInt(31, 1)...)      // isOriginSource
	out := pb.EncodeInt(1, MsgTypeImage)
	out = append(out, pb.EncodeBytes(2, content)...)
	out = append(out, pb.EncodeString(3, chatID)...)
	out = append(out, pb.EncodeString(6, pb.NewUUID())...)
	out = append(out, pb.EncodeInt(7, 1)...)
	out = append(out, pb.EncodeInt(8, 1)...)
	out = append(out, pb.EncodeInt(9, 1)...)
	return out
}

func EncodePutFile(chatID, key, name, mimeType string, size int) []byte {
	content := pb.EncodeString(6, key)
	content = append(content, pb.EncodeString(11, name)...)
	content = append(content, pb.EncodeString(12, mimeType)...)
	content = append(content, pb.EncodeInt(13, uint64(size))...)
	out := pb.EncodeInt(1, MsgTypeFile)
	out = append(out, pb.EncodeBytes(2, content)...)
	out = append(out, pb.EncodeString(3, chatID)...)
	out = append(out, pb.EncodeString(6, pb.NewUUID())...)
	out = append(out, pb.EncodeInt(7, 1)...)
	out = append(out, pb.EncodeInt(8, 1)...)
	return out
}

type uploadResp struct {
	Code int `json:"code"`
	File struct {
		FileKey string `json:"file_key"`
		Size    int    `json:"size"`
		Name    string `json:"name"`
		Mime    string `json:"mime"`
	} `json:"file"`
}

func UploadImage(auth session.Auth, path string) (key, mimeType, name string, size int, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", "", 0, err
	}
	if len(data) == 0 {
		return "", "", "", 0, fmt.Errorf("empty image file")
	}
	name = filepath.Base(path)
	mimeType = mimeForPath(path)
	if _, _, err := image.DecodeConfig(bytes.NewReader(data)); err != nil && !strings.HasSuffix(strings.ToLower(path), ".webp") {
		return "", "", "", 0, fmt.Errorf("not a readable image: %w", err)
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	meta, _ := json.Marshal(map[string]any{
		"mime":        mimeType,
		"name":        name,
		"size":        len(data),
		"file_type":   uploadFileType,
		"mount_point": uploadMountPoint,
	})
	if err := w.WriteField("data", string(meta)); err != nil {
		return "", "", "", 0, err
	}
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, name))
	h.Set("Content-Type", mimeType)
	fw, err := w.CreatePart(h)
	if err != nil {
		return "", "", "", 0, err
	}
	if _, err := fw.Write(data); err != nil {
		return "", "", "", 0, err
	}
	if err := w.Close(); err != nil {
		return "", "", "", 0, err
	}
	req, err := http.NewRequest(http.MethodPost, uploadURL, &buf)
	if err != nil {
		return "", "", "", 0, err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Cookie", auth.CookieHeader())
	req.Header.Set("User-Agent", "larkdesk/0.1")
	req.Header.Set("Origin", "https://www.feishu.cn")
	req.Header.Set("Referer", "https://www.feishu.cn/messenger/")
	resp, err := httpx.Client(60 * time.Second).Do(req)
	if err != nil {
		return "", "", "", 0, fmt.Errorf("upload image: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", "", 0, err
	}
	if resp.StatusCode >= 400 {
		return "", "", "", 0, fmt.Errorf("upload image HTTP %d", resp.StatusCode)
	}
	var out uploadResp
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", "", "", 0, fmt.Errorf("upload image: bad json")
	}
	if out.Code != 0 || out.File.FileKey == "" {
		return "", "", "", 0, fmt.Errorf("upload image failed: %s", strings.TrimSpace(string(raw)))
	}
	return out.File.FileKey, mimeType, name, len(data), nil
}

func Image(auth session.Auth, who, path string, dryRun bool) (map[string]any, error) {
	if !auth.HasSessionCookie() {
		return nil, fmt.Errorf("no decrypted session cookie")
	}
	person, err := ResolvePerson(auth, who)
	if err != nil {
		return nil, err
	}
	chatID, err := PutP2PChat(auth, person.ID)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	out := map[string]any{
		"to":      person.AsMap(),
		"chat_id": chatID,
		"image":   path,
		"dry_run": dryRun,
	}
	if dryRun {
		return out, nil
	}
	key, mimeType, name, size, err := UploadImage(auth, path)
	if err != nil {
		return nil, err
	}
	out["key"] = key
	out["mime"] = mimeType
	status, raw, err := gateway.Do(auth, CmdPutMessage, EncodePutImage(chatID, key))
	if err != nil {
		return nil, err
	}
	kind := "image"
	if status != 200 || bytes.Contains(raw, []byte("ValidateKey")) {
		status, raw, err = gateway.Do(auth, CmdPutMessage, EncodePutFile(chatID, key, name, mimeType, size))
		if err != nil {
			return nil, err
		}
		if status >= 400 {
			return nil, fmt.Errorf("gateway HTTP %d cmd=%d", status, CmdPutMessage)
		}
		kind = "file"
	}
	out["message_id"] = ParsePutMessageResponse(pb.DecodePacket(raw).Payload)
	if out["message_id"] == "" {
		out["message_id"] = ParsePutMessageResponse(raw)
	}
	out["sent"] = true
	out["kind"] = kind
	return out, nil
}
