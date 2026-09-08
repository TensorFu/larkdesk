package search

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/TensorFu/larkdesk/internal/contacts"
	"github.com/TensorFu/larkdesk/internal/gateway"
	"github.com/TensorFu/larkdesk/internal/pb"
	"github.com/TensorFu/larkdesk/internal/session"
)

const (
	CmdUniversalSearch = 11021
	EntityUser         = 1
)

var highlight = regexp.MustCompile(`</?h>`)

func EncodeRequest(query string, pageSize int) ([]byte, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("empty search query")
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	entity := pb.EncodeInt(1, EntityUser)
	ctx := append(pb.EncodeString(1, "SEARCH_CHATTERS"), pb.EncodeBytes(2, entity)...)
	header := pb.EncodeString(1, pb.NewUUID())
	header = append(header, pb.EncodeInt(2, 1)...)
	header = append(header, pb.EncodeString(3, query)...)
	header = append(header, pb.EncodeBytes(5, ctx)...)
	header = append(header, pb.EncodeString(6, "zh_CN")...)
	header = append(header, pb.EncodeInt(11, uint64(pageSize))...)
	return pb.EncodeBytes(1, header), nil
}

func ParseResponse(payload []byte) []contacts.Contact {
	var found []contacts.Contact
	seen := map[string]bool{}
	for _, f := range pb.DecodeFields(payload) {
		if f.Num != 2 || f.Wire != "bytes" {
			continue
		}
		ident, name := "", ""
		entType := uint64(0)
		for _, inner := range pb.DecodeFields(f.Bytes) {
			switch inner.Num {
			case 1:
				if s, ok := pb.UTF8Printable(inner.Bytes); ok {
					ident = s
				}
			case 2:
				if inner.Wire == "varint" {
					entType = inner.Int
				}
			case 3:
				if s, ok := pb.UTF8Printable(inner.Bytes); ok {
					name = strings.TrimSpace(highlight.ReplaceAllString(s, ""))
				}
			}
		}
		if entType != 0 && entType != EntityUser {
			continue
		}
		if name == "" || ident == "" || seen[ident] {
			continue
		}
		seen[ident] = true
		found = append(found, contacts.Contact{Name: name, ID: ident})
	}
	return found
}

func Contacts(auth session.Auth, query string) ([]contacts.Contact, error) {
	if !auth.HasSessionCookie() {
		return nil, fmt.Errorf("no decrypted session cookie for search")
	}
	payload, err := EncodeRequest(query, 20)
	if err != nil {
		return nil, err
	}
	raw, err := gateway.PostCommand(auth, CmdUniversalSearch, payload)
	if err != nil {
		return nil, err
	}
	return ParseResponse(raw), nil
}
