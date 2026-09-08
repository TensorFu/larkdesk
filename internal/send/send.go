package send

import (
	"bytes"
	"fmt"
	"strings"
	"unicode"

	"github.com/TensorFu/larkdesk/internal/contacts"
	"github.com/TensorFu/larkdesk/internal/gateway"
	"github.com/TensorFu/larkdesk/internal/pb"
	"github.com/TensorFu/larkdesk/internal/search"
	"github.com/TensorFu/larkdesk/internal/session"
)

const (
	CmdPutP2PChats = 50
	CmdPutMessage  = 5
	MsgTypeText    = 4
	TagText        = 1
)

func isDigitID(s string) bool {
	if len(s) < 10 {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func ResolvePerson(auth session.Auth, who string) (contacts.Contact, error) {
	who = strings.TrimSpace(who)
	hits, err := search.Contacts(auth, who)
	if err != nil {
		return contacts.Contact{}, err
	}
	if isDigitID(who) {
		for _, h := range hits {
			if h.ID == who {
				return h, nil
			}
		}
		return contacts.Contact{Name: who, ID: who}, nil
	}
	var exact []contacts.Contact
	for _, h := range hits {
		if h.Name == who {
			exact = append(exact, h)
		}
	}
	pool := exact
	if len(pool) == 0 {
		pool = hits
	}
	if len(pool) == 0 {
		return contacts.Contact{}, fmt.Errorf("no contact matched %q", who)
	}
	if len(pool) > 1 {
		parts := make([]string, 0, 8)
		for i, c := range pool {
			if i >= 8 {
				break
			}
			parts = append(parts, fmt.Sprintf("%s(%s)", c.Name, c.ID))
		}
		return contacts.Contact{}, fmt.Errorf("multiple contacts for %q: %s", who, strings.Join(parts, ", "))
	}
	return pool[0], nil
}

func PutP2PChat(auth session.Auth, userID string) (string, error) {
	raw, err := gateway.PostCommand(auth, CmdPutP2PChats, pb.EncodeString(1, userID))
	if err != nil {
		return "", err
	}
	fields := pb.DecodeFields(raw)
	if len(fields) == 0 {
		return "", fmt.Errorf("PUT_P2P_CHATS empty response")
	}
	for _, f := range fields {
		if f.Num != 1 || f.Wire != "bytes" {
			continue
		}
		for _, inner := range pb.DecodeFields(f.Bytes) {
			if inner.Num == 1 && inner.Wire == "bytes" {
				if s, ok := pb.UTF8Printable(inner.Bytes); ok && isDigitID(s) {
					return s, nil
				}
			}
		}
	}
	return "", fmt.Errorf("PUT_P2P_CHATS missing chat id")
}

func EncodeRichText(text string) []byte {
	eid := pb.NewUUID()
	prop := pb.EncodeBytes(1, pb.EncodeString(1, text))
	element := append(pb.EncodeInt(1, TagText), pb.EncodeBytes(3, prop)...)
	dictionary := append(pb.EncodeString(1, eid), pb.EncodeBytes(2, element)...)
	elements := pb.EncodeBytes(1, dictionary)
	out := pb.EncodeString(1, eid)
	out = append(out, pb.EncodeString(2, text)...)
	out = append(out, pb.EncodeBytes(3, elements)...)
	return out
}

func EncodePutMessage(chatID, text string) []byte {
	content := pb.EncodeBytes(1, EncodeRichText(text))
	out := pb.EncodeInt(1, MsgTypeText)
	out = append(out, pb.EncodeBytes(2, content)...)
	out = append(out, pb.EncodeString(3, chatID)...)
	return out
}

func ParsePutMessageResponse(payload []byte) string {
	for _, f := range pb.DecodeFields(payload) {
		if f.Num != 1 || f.Wire != "bytes" {
			continue
		}
		for _, inner := range pb.DecodeFields(f.Bytes) {
			if inner.Num == 1 && inner.Wire == "bytes" {
				if s, ok := pb.UTF8Printable(inner.Bytes); ok {
					return s
				}
			}
		}
	}
	return ""
}

func Text(auth session.Auth, who, text string, dryRun bool) (map[string]any, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("empty message")
	}
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
	out := map[string]any{
		"to":      person.AsMap(),
		"chat_id": chatID,
		"text":    text,
		"dry_run": dryRun,
	}
	if dryRun {
		return out, nil
	}
	raw, err := gateway.PostCommand(auth, CmdPutMessage, EncodePutMessage(chatID, text))
	if err != nil {
		return nil, err
	}
	if !bytes.Contains(raw, []byte(text)) {
		return nil, fmt.Errorf("PUT_MESSAGE succeeded but body did not echo the text")
	}
	out["message_id"] = ParsePutMessageResponse(raw)
	out["sent"] = true
	return out, nil
}
