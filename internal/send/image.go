package send

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
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

	"github.com/TensorFu/larkdesk/internal/frontier"
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
	cryptoAES256GCM  = 1
	cropOrigin       = 1
	cropMiddle       = 2
	cropThumb        = 3
	defaultFSUnit    = "eu_nc-cdn"
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

func cropAttr(kind, w, h int) []byte {
	a := pb.EncodeInt(1, uint64(w))
	a = append(a, pb.EncodeInt(2, uint64(h))...)
	item := pb.EncodeInt(1, uint64(kind))
	return append(item, pb.EncodeBytes(2, a)...)
}

func jpegPreview(img image.Image) []byte {
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 40})
	return buf.Bytes()
}

func EncodeImageContent(key, fsUnit string, w, h, originSize int, secret, nonce, preview []byte) []byte {
	if fsUnit == "" {
		fsUnit = defaultFSUnit
	}
	cipherMsg := pb.EncodeBytes(1, secret)
	cipherMsg = append(cipherMsg, pb.EncodeBytes(2, nonce)...)
	cipherMsg = append(cipherMsg, pb.EncodeBytes(3, nil)...)
	crypto := pb.EncodeInt(1, cryptoAES256GCM)
	crypto = append(crypto, pb.EncodeBytes(2, cipherMsg)...)
	v2 := pb.EncodeString(1, key)
	v2 = append(v2, pb.EncodeString(2, fsUnit)...)
	v2 = append(v2, pb.EncodeBytes(3, crypto)...)
	v2 = append(v2, pb.EncodeBytes(4, cropAttr(cropOrigin, w, h))...)
	v2 = append(v2, pb.EncodeBytes(4, cropAttr(cropMiddle, w, h))...)
	v2 = append(v2, pb.EncodeBytes(4, cropAttr(cropThumb, w, h))...)
	if len(preview) > 0 {
		v2 = append(v2, pb.EncodeBytes(5, preview)...)
	}
	out := pb.EncodeBytes(2, v2)
	out = append(out, pb.EncodeInt(3, 1)...)
	out = append(out, pb.EncodeInt(4, uint64(originSize))...)
	return out
}

func EncodePutImage(chatID, key string) []byte {
	secret := make([]byte, 32)
	nonce := make([]byte, 12)
	content := EncodeImageContent(key, defaultFSUnit, 1, 1, 1, secret, nonce, nil)
	return encodePut(MsgTypeImage, chatID, content)
}

func encodePut(typ uint64, chatID string, content []byte) []byte {
	out := pb.EncodeInt(1, typ)
	out = append(out, pb.EncodeBytes(2, content)...)
	out = append(out, pb.EncodeString(3, chatID)...)
	out = append(out, pb.EncodeString(6, pb.NewUUID())...)
	out = append(out, pb.EncodeInt(7, 1)...)
	out = append(out, pb.EncodeInt(8, 1)...)
	return out
}

func EncodePutFile(chatID, key, name, mimeType string, size int) []byte {
	content := pb.EncodeString(6, key)
	content = append(content, pb.EncodeString(11, name)...)
	content = append(content, pb.EncodeString(12, mimeType)...)
	content = append(content, pb.EncodeInt(13, uint64(size))...)
	return encodePut(MsgTypeFile, chatID, content)
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
	return uploadBytes(auth, data, filepath.Base(path), mimeForPath(path))
}

func uploadBytes(auth session.Auth, data []byte, name, mimeType string) (key, mime string, filename string, size int, err error) {
	if len(data) == 0 {
		return "", "", "", 0, fmt.Errorf("empty image file")
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

func aesGCM(plain []byte) (secret, nonce, ct []byte, err error) {
	secret = make([]byte, 32)
	nonce = make([]byte, 12)
	if _, err = rand.Read(secret); err != nil {
		return nil, nil, nil, err
	}
	if _, err = rand.Read(nonce); err != nil {
		return nil, nil, nil, err
	}
	block, err := aes.NewCipher(secret)
	if err != nil {
		return nil, nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(nonce) != gcm.NonceSize() {
		nonce = make([]byte, gcm.NonceSize())
		_, _ = rand.Read(nonce)
	}
	ct = gcm.Seal(nil, nonce, plain, nil)
	return secret, nonce, ct, nil
}

func putImageOK(status int, raw []byte, key string) bool {
	if status != 200 || len(raw) < 80 {
		return false
	}
	if bytes.Contains(raw, []byte("ValidateKey")) {
		return false
	}
	return bytes.Contains(raw, []byte(key)) || len(raw) > 200
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
	plain, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(plain) == 0 {
		return nil, fmt.Errorf("empty image file")
	}
	cfg, img, err := decodeImage(plain)
	if err != nil {
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
	secret, nonce, _, err := aesGCM(plain)
	if err != nil {
		return nil, err
	}
	key, mimeType, name, size, err := uploadBytes(auth, plain, filepath.Base(path), mimeForPath(path))
	if err != nil {
		return nil, err
	}
	out["key"] = key
	out["mime"] = mimeType
	preview := jpegPreview(img)
	content := EncodeImageContent(key, defaultFSUnit, cfg.Width, cfg.Height, size, secret, nonce, preview)
	put := encodePut(MsgTypeImage, chatID, content)

	kind := "image"
	status, raw, err := gateway.Do(auth, CmdPutMessage, put)
	if err != nil {
		return nil, err
	}
	if !putImageOK(status, raw, key) {
		if raw2, err2 := frontier.Call(auth, CmdPutMessage, put, []byte(key)); err2 == nil && len(raw2) > 0 {
			raw = raw2
			status = 200
		}
	}
	if !putImageOK(status, raw, key) {
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

func decodeImage(plain []byte) (image.Config, image.Image, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(plain))
	if err != nil {
		return image.Config{}, nil, fmt.Errorf("not a readable image: %w", err)
	}
	img, _, err := image.Decode(bytes.NewReader(plain))
	if err != nil {
		return cfg, image.NewRGBA(image.Rect(0, 0, cfg.Width, cfg.Height)), nil
	}
	return cfg, img, nil
}
