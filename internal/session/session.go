package session

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/pbkdf2"
	"crypto/sha1"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

const (
	profileKeyPrefix          = "CURRENT_PROFILE_INFO__"
	tenantKeyPrefix           = "CURRENT_TENANT_INFO__"
	chromiumHashPrefixVersion = 24
)

var sessionCookieNames = map[string]bool{
	"session": true, "osession": true, "sl_session": true, "session_list": true,
}

type Identity struct {
	UserID     string `json:"id"`
	Name       string `json:"name"`
	TenantID   string `json:"tenant_id,omitempty"`
	TenantName string `json:"tenant,omitempty"`
	Domain     string `json:"domain,omitempty"`
}

type Auth struct {
	Identity Identity
	Cookies  map[string]string
	Source   string
}

func (a Auth) CookieHeader() string {
	parts := make([]string, 0, len(a.Cookies))
	for name, value := range a.Cookies {
		if value != "" {
			parts = append(parts, name+"="+value)
		}
	}
	return strings.Join(parts, "; ")
}

func (a Auth) HasSessionCookie() bool {
	h := a.CookieHeader()
	return strings.Contains(h, "session=") || strings.Contains(h, "osession=") || strings.Contains(h, "sl_session=")
}

func DefaultLarkShell() string {
	if v := os.Getenv("LARKDESK_LARKSHELL"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library/Application Support/LarkShell")
}

func UserDirHash(userID string) string {
	sum := md5.Sum([]byte(userID))
	return hex.EncodeToString(sum[:])
}

func sqliteRO(path string) string {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: "mode=ro"}
	return u.String()
}

func openRO(path string) (*sql.DB, error) {
	return sql.Open("sqlite", sqliteRO(path))
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

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func str(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if m == nil {
			return ""
		}
		if v, ok := m[k]; ok && v != nil {
			return fmt.Sprint(v)
		}
	}
	return ""
}

func LoadIdentity(larkshell string) (Identity, error) {
	db := filepath.Join(larkshell, "persistent_storage.db")
	if _, err := os.Stat(db); err != nil {
		return Identity{}, fmt.Errorf("missing desktop session store: %s", db)
	}
	con, err := openRO(db)
	if err != nil {
		return Identity{}, fmt.Errorf("cannot open desktop session store: %w", err)
	}
	defer con.Close()

	rows, err := con.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name LIKE 'kv_%'`)
	if err != nil {
		return Identity{}, fmt.Errorf("desktop session store is unreadable: %w", err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err == nil {
			tables = append(tables, name)
		}
	}
	rows.Close()

	profile := map[string]any{}
	tenant := map[string]any{}
	profileKeyUserID := ""
	for _, table := range tables {
		cols, err := con.Query("PRAGMA table_info(" + table + ")")
		if err != nil {
			continue
		}
		hasKey, hasValue := false, false
		for cols.Next() {
			var cid int
			var name, ctype string
			var notnull, pk int
			var dflt any
			if err := cols.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
				continue
			}
			if name == "key" {
				hasKey = true
			}
			if name == "value" {
				hasValue = true
			}
		}
		cols.Close()
		if !hasKey || !hasValue {
			continue
		}
		kv, err := con.Query("SELECT key, value FROM " + table)
		if err != nil {
			continue
		}
		for kv.Next() {
			var key, value string
			if err := kv.Scan(&key, &value); err != nil {
				continue
			}
			if strings.HasPrefix(key, profileKeyPrefix) {
				if profileKeyUserID == "" {
					profileKeyUserID = key[len(profileKeyPrefix):]
				}
				if len(profile) == 0 {
					var parsed any
					if json.Unmarshal([]byte(value), &parsed) == nil {
						if m := asMap(unwrapOrgVal(parsed)); m != nil {
							profile = m
						}
					}
				}
			}
			if strings.HasPrefix(key, tenantKeyPrefix) && len(tenant) == 0 {
				var parsed any
				if json.Unmarshal([]byte(value), &parsed) == nil {
					if m := asMap(unwrapOrgVal(parsed)); m != nil {
						tenant = m
					}
				}
			}
			if key == "passportTenantInfo" && len(tenant) == 0 {
				var parsed any
				if json.Unmarshal([]byte(value), &parsed) == nil {
					un := unwrapOrgVal(parsed)
					if m := asMap(un); m != nil {
						if data := asMap(m["data"]); data != nil {
							tenant = data
						} else {
							tenant = m
						}
					}
				}
			}
		}
		kv.Close()
	}

	userID := str(tenant, "userId", "user_id")
	if userID == "" {
		userID = profileKeyUserID
	}
	name := str(profile, "localizedName", "nameWithAnotherName", "name")
	if userID == "" || name == "" {
		return Identity{}, fmt.Errorf("no logged-in Feishu desktop identity (open Lark.app and sign in first)")
	}
	return Identity{
		UserID:     userID,
		Name:       name,
		TenantID:   firstNonEmpty(str(profile, "tenantId"), str(tenant, "tenantId")),
		TenantName: firstNonEmpty(str(tenant, "description"), str(tenant, "tenantName")),
		Domain:     str(tenant, "suiteFullDomain"),
	}, nil
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

func chromiumDBVersion(path string) int {
	con, err := openRO(path)
	if err != nil {
		return 0
	}
	defer con.Close()
	var v string
	if err := con.QueryRow("SELECT value FROM meta WHERE key='version'").Scan(&v); err != nil {
		return 0
	}
	n := 0
	fmt.Sscanf(v, "%d", &n)
	return n
}

func unpadPKCS7(data []byte) []byte {
	if len(data) == 0 {
		return data
	}
	pad := int(data[len(data)-1])
	if pad < 1 || pad > 16 || pad > len(data) {
		return data
	}
	for i := 0; i < pad; i++ {
		if data[len(data)-1-i] != byte(pad) {
			return data
		}
	}
	return data[:len(data)-pad]
}

func DeriveChromiumKey(password string) ([]byte, error) {
	return pbkdf2.Key(sha1.New, password, []byte("saltysalt"), 1003, 16)
}

func DecryptChromiumValue(encrypted []byte, password, hostKey string, dbVersion int) (string, error) {
	if len(encrypted) == 0 {
		return "", nil
	}
	if len(encrypted) < 3 || (string(encrypted[:3]) != "v10" && string(encrypted[:3]) != "v11") {
		s := string(encrypted)
		if !utf8Printable(s) {
			return "", fmt.Errorf("cookie is not Chromium v10 and is not utf-8")
		}
		return s, nil
	}
	key, err := DeriveChromiumKey(password)
	if err != nil {
		return "", err
	}
	payload := encrypted[3:]
	if len(payload) < 16 || len(payload)%16 != 0 {
		return "", fmt.Errorf("truncated Chromium cookie ciphertext")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	plain := make([]byte, len(payload))
	cipher.NewCBCDecrypter(block, []byte("                ")).CryptBlocks(plain, payload)
	plain = unpadPKCS7(plain)
	if dbVersion >= chromiumHashPrefixVersion && len(plain) >= 32 {
		sum := sha256.Sum256([]byte(hostKey))
		if string(plain[:32]) == string(sum[:]) {
			plain = plain[32:]
		}
	}
	if !utf8.Valid(plain) {
		return "", fmt.Errorf("cookie decrypted to non-utf8 (wrong desktop key)")
	}
	s := string(plain)
	if !utf8Printable(s) {
		return "", fmt.Errorf("cookie decrypted to non-printable data (wrong desktop key)")
	}
	return s, nil
}

func utf8Printable(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r == '\n' || r == '\r' || r == '\t' {
			continue
		}
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}

func keychainPassword(service, account string) string {
	out, err := exec.Command("security", "find-generic-password", "-s", service, "-a", account, "-w").Output()
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(out), "\n")
}

func CandidatePasswords() [][2]string {
	if env := os.Getenv("LARKDESK_COOKIE_PASSWORD"); env != "" {
		return [][2]string{{"env", env}}
	}
	if os.Getenv("LARKDESK_TRY_KEYCHAIN") == "0" {
		return nil
	}
	pairs := [][2]string{
		{"Chrome Safe Storage", "Chrome"},
		{"Chromium Safe Storage", "Chromium"},
		{"Suite App Safe Storage", "XY6NLV7YTS.com.electron.lark-SuiteApp"},
		{"Suite App Safe Storage", "XY6NLV7YTS.com.electron.lark.iron-SuiteApp"},
	}
	var found [][2]string
	for _, p := range pairs {
		if pw := keychainPassword(p[0], p[1]); pw != "" {
			found = append(found, [2]string{"keychain:" + p[0] + ":" + p[1], pw})
		}
	}
	return found
}

func CookiesPathForUser(larkshell, userID string) string {
	return filepath.Join(larkshell, "aha", "users", UserDirHash(userID), "profile_explorer", "Cookies")
}

func SessionCookieRowsExist(path string) bool {
	if _, err := os.Stat(path); err != nil {
		return false
	}
	con, err := openRO(path)
	if err != nil {
		return false
	}
	defer con.Close()
	var n int
	err = con.QueryRow("SELECT 1 FROM cookies WHERE name IN ('session','osession','sl_session') LIMIT 1").Scan(&n)
	return err == nil
}

func LoadSessionCookies(path string, passwords [][2]string) (map[string]string, string, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, "", fmt.Errorf("missing desktop cookie store: %s", path)
	}
	con, err := openRO(path)
	if err != nil {
		return nil, "", fmt.Errorf("cannot read desktop cookie store: %w", err)
	}
	defer con.Close()
	dbVersion := chromiumDBVersion(path)
	rows, err := con.Query("SELECT host_key, name, value, encrypted_value FROM cookies")
	if err != nil {
		return nil, "", fmt.Errorf("cannot read desktop cookie store: %w", err)
	}
	defer rows.Close()

	type crow struct {
		host, name, value string
		enc               []byte
	}
	var sessionRows []crow
	for rows.Next() {
		var r crow
		if err := rows.Scan(&r.host, &r.name, &r.value, &r.enc); err != nil {
			continue
		}
		if sessionCookieNames[r.name] {
			sessionRows = append(sessionRows, r)
		}
	}
	if len(sessionRows) == 0 {
		return nil, "", fmt.Errorf("desktop cookie store has no session cookies; log into Lark.app")
	}
	plain := map[string]string{}
	for _, r := range sessionRows {
		if r.value != "" {
			plain[r.name] = r.value
		}
	}
	if plain["session"] != "" || plain["osession"] != "" {
		return plain, "plaintext", nil
	}
	if passwords == nil {
		passwords = CandidatePasswords()
	}
	var last error
	for _, pw := range passwords {
		decoded := map[string]string{}
		for k, v := range plain {
			decoded[k] = v
		}
		ok := true
		for _, r := range sessionRows {
			if len(r.enc) == 0 {
				continue
			}
			text, err := DecryptChromiumValue(r.enc, pw[1], r.host, dbVersion)
			if err != nil {
				last = err
				ok = false
				break
			}
			decoded[r.name] = text
		}
		if ok && (decoded["session"] != "" || decoded["osession"] != "" || decoded["sl_session"] != "") {
			return decoded, pw[0], nil
		}
	}
	if last != nil {
		return nil, "", fmt.Errorf("desktop session cookies are present but could not be decrypted (%v)", last)
	}
	return nil, "", fmt.Errorf("desktop session cookies could not be decrypted")
}

func Load(larkshell string, passwords [][2]string) (Auth, error) {
	if larkshell == "" {
		larkshell = DefaultLarkShell()
	}
	st, err := os.Stat(larkshell)
	if err != nil || !st.IsDir() {
		return Auth{}, fmt.Errorf("Lark desktop data directory not found: %s", larkshell)
	}
	ident, err := LoadIdentity(larkshell)
	if err != nil {
		return Auth{}, err
	}
	cpath := CookiesPathForUser(larkshell, ident.UserID)
	if !SessionCookieRowsExist(cpath) {
		return Auth{}, fmt.Errorf("no Chromium session cookies for this user; log into Lark.app first")
	}
	cookies, source, err := LoadSessionCookies(cpath, passwords)
	if err != nil {
		return Auth{Identity: ident, Cookies: map[string]string{}, Source: "encrypted-present"}, nil
	}
	return Auth{Identity: ident, Cookies: cookies, Source: source}, nil
}
