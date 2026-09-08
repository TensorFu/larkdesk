package contacts

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/TensorFu/larkdesk/internal/gateway"
	"github.com/TensorFu/larkdesk/internal/pb"
	"github.com/TensorFu/larkdesk/internal/session"

	_ "modernc.org/sqlite"
)

const CmdPullContactsV2 = 1100310

type Contact struct {
	Name       string `json:"name"`
	ID         string `json:"id"`
	Department string `json:"department,omitempty"`
	Tenant     string `json:"tenant,omitempty"`
	TenantID   string `json:"tenant_id,omitempty"`
}

func (c Contact) AsMap() map[string]any {
	out := map[string]any{"name": c.Name, "id": c.ID}
	if c.Department != "" {
		out["department"] = c.Department
	}
	if c.Tenant != "" {
		out["tenant"] = c.Tenant
	}
	if c.TenantID != "" {
		out["tenant_id"] = c.TenantID
	}
	return out
}

func unwrapOrgVal(raw any) any {
	switch v := raw.(type) {
	case map[string]any:
		if inner, ok := v["orgVal"]; ok {
			if s, ok := inner.(string); ok {
				var parsed any
				if json.Unmarshal([]byte(s), &parsed) == nil {
					return unwrapOrgVal(parsed)
				}
				return inner
			}
			return unwrapOrgVal(inner)
		}
	case string:
		var parsed any
		if json.Unmarshal([]byte(v), &parsed) == nil {
			return unwrapOrgVal(parsed)
		}
	}
	return raw
}

func looksLikeDepartment(obj map[string]any) bool {
	if obj["parentId"] == nil && obj["parent_id"] == nil {
		return false
	}
	_, a := obj["avatarKey"]
	_, b := obj["avatar_key"]
	return !a && !b
}

func str(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			s := strings.TrimSpace(fmt.Sprint(v))
			if s != "" && s != "<nil>" {
				return s
			}
		}
	}
	return ""
}

func contactFromMapping(obj map[string]any) (Contact, bool) {
	name := str(obj, "localizedName", "nameWithAnotherName", "localized_name", "name", "displayName")
	ident := str(obj, "open_id", "openId", "userId", "user_id", "chatterId", "chatter_id", "id")
	if name == "" || ident == "" || looksLikeDepartment(obj) {
		return Contact{}, false
	}
	c := Contact{Name: name, ID: ident}
	c.Department = str(obj, "departmentName", "department")
	c.TenantID = str(obj, "tenantId")
	return c, true
}

func ParseContactPayload(payload any) []Contact {
	seen := map[string]bool{}
	var found []Contact
	add := func(c Contact, ok bool) {
		if !ok || seen[c.ID] {
			return
		}
		seen[c.ID] = true
		found = append(found, c)
	}
	var walk func(any)
	walk = func(node any) {
		switch n := node.(type) {
		case map[string]any:
			if ent, ok := n["entity"].(map[string]any); ok {
				add(contactFromMapping(ent))
			}
			add(contactFromMapping(n))
			for k, v := range n {
				switch k {
				case "users", "contacts", "entries", "dataSource", "chatters", "items", "data":
					walk(v)
				default:
					switch v.(type) {
					case map[string]any, []any:
						walk(v)
					}
				}
			}
		case []any:
			for _, item := range n {
				walk(item)
			}
		case string:
			var parsed any
			if json.Unmarshal([]byte(n), &parsed) == nil {
				walk(parsed)
			}
		}
	}
	walk(unwrapOrgVal(payload))
	return found
}

func LoadLocal(larkshell string) ([]Contact, error) {
	db := filepath.Join(larkshell, "persistent_storage.db")
	if _, err := os.Stat(db); err != nil {
		return nil, fmt.Errorf("missing desktop session store: %s", db)
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(db), RawQuery: "mode=ro"}
	con, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("cannot read desktop contact cache: %w", err)
	}
	defer con.Close()
	rows, err := con.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name LIKE 'kv_%'`)
	if err != nil {
		return nil, fmt.Errorf("cannot read desktop contact cache: %w", err)
	}
	var tables []string
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil {
			tables = append(tables, n)
		}
	}
	rows.Close()
	var contacts []Contact
	tokens := []string{"localizedName", "nameWithAnotherName", "avatarKey", "open_id"}
	for _, table := range tables {
		kv, err := con.Query("SELECT key, value FROM " + table)
		if err != nil {
			continue
		}
		for kv.Next() {
			var key, value string
			if kv.Scan(&key, &value) != nil {
				continue
			}
			ok := false
			for _, t := range tokens {
				if strings.Contains(value, t) {
					ok = true
					break
				}
			}
			if !ok {
				continue
			}
			var payload any
			if json.Unmarshal([]byte(value), &payload) != nil {
				continue
			}
			contacts = append(contacts, ParseContactPayload(payload)...)
		}
		kv.Close()
	}
	return Dedupe(contacts), nil
}

func Dedupe(in []Contact) []Contact {
	seen := map[string]bool{}
	var out []Contact
	for _, c := range in {
		if seen[c.ID] {
			continue
		}
		seen[c.ID] = true
		out = append(out, c)
	}
	return out
}

func ParsePullContactsV2(payload []byte) []Contact {
	var contacts []Contact
	for _, f := range pb.DecodeFields(payload) {
		if f.Num != 4 || f.Wire != "bytes" {
			continue
		}
		name, ident, tenant := "", "", ""
		for _, inner := range pb.DecodeFields(f.Bytes) {
			if inner.Wire != "bytes" {
				continue
			}
			by := map[int][]byte{}
			for _, n := range pb.DecodeFields(inner.Bytes) {
				if n.Wire == "bytes" {
					by[n.Num] = n.Bytes
				}
			}
			if inner.Num == 1 {
				if s, ok := pb.UTF8Printable(by[2]); ok {
					name = s
				}
				if s, ok := pb.UTF8Printable(by[6]); ok {
					ident = s
				}
				if s, ok := pb.UTF8Printable(by[4]); ok {
					tenant = s
				}
			} else if inner.Num == 3 {
				if ident == "" {
					if s, ok := pb.UTF8Printable(by[1]); ok {
						ident = s
					}
				}
				if name == "" {
					if s, ok := pb.UTF8Printable(by[2]); ok {
						name = s
					}
				}
			}
		}
		if name != "" && ident != "" {
			contacts = append(contacts, Contact{Name: name, ID: ident, Tenant: tenant})
		}
	}
	return Dedupe(contacts)
}

func FetchRemote(auth session.Auth) ([]Contact, error) {
	raw, err := gateway.PostCommand(auth, CmdPullContactsV2, nil)
	if err != nil {
		return nil, err
	}
	parsed := ParsePullContactsV2(raw)
	if len(parsed) == 0 {
		return nil, fmt.Errorf("PULL_CONTACTS_V2 returned no contacts")
	}
	return parsed, nil
}

func List(auth session.Auth, larkshell string) ([]Contact, string, error) {
	if larkshell == "" {
		larkshell = session.DefaultLarkShell()
	}
	local, err := LoadLocal(larkshell)
	if err != nil {
		return nil, "", err
	}
	self := Contact{Name: auth.Identity.Name, ID: auth.Identity.UserID}
	out := Dedupe(append([]Contact{self}, local...))
	source := "desktop-session"
	if auth.HasSessionCookie() && auth.Identity.Domain != "fixture.feishu.cn" {
		if remote, err := FetchRemote(auth); err == nil {
			out = Dedupe(append(remote, out...))
			source = "pull_contacts_v2"
		}
	}
	if len(out) == 0 {
		return nil, "", fmt.Errorf("desktop session produced an empty contact list (auth/permission failure)")
	}
	return out, source, nil
}
