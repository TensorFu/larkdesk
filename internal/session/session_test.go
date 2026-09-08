package session

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

const (
	fixturePassword = "fixture-desktop-password"
	fixtureUserID   = "10001"
	fixtureName     = "Fixture User"
	fixtureSession  = "fixture-session-token"
	host            = ".feishu.cn"
)

func encryptV10(plain []byte, password string) []byte {
	key, err := DeriveChromiumKey(password)
	if err != nil {
		panic(err)
	}
	pad := 16 - (len(plain) % 16)
	padded := append(plain, bytesOf(byte(pad), pad)...)
	block, _ := aes.NewCipher(key)
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, []byte("                ")).CryptBlocks(out, padded)
	return append([]byte("v10"), out...)
}

func bytesOf(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

func writePersistentDB(t *testing.T, root, userID, name string) {
	t.Helper()
	db := filepath.Join(root, "persistent_storage.db")
	con, err := sql.Open("sqlite", db)
	if err != nil {
		t.Fatal(err)
	}
	defer con.Close()
	_, _ = con.Exec("CREATE TABLE kv_meta (user_id TEXT, partition TEXT, storage_id TEXT)")
	_, _ = con.Exec("INSERT INTO kv_meta VALUES ('10001','default','1')")
	_, _ = con.Exec("CREATE TABLE kv_default (storage_id TEXT, key TEXT, value TEXT, partition TEXT, update_time TEXT)")
	profile := `{"orgVal":"{\"localizedName\":\"` + name + `\",\"nameWithAnotherName\":\"` + name + `\",\"tenantId\":\"tenant-1\"}"}`
	tenant := `{"orgVal":"{\"userId\":\"` + userID + `\",\"description\":\"Fixture Tenant\",\"suiteFullDomain\":\"fixture.feishu.cn\"}"}`
	_, _ = con.Exec("INSERT INTO kv_default VALUES (?,?,?,?,?)", "1", "CURRENT_PROFILE_INFO__"+userID, profile, "default", "0")
	_, _ = con.Exec("INSERT INTO kv_default VALUES (?,?,?,?,?)", "1", "CURRENT_TENANT_INFO__"+userID, tenant, "default", "0")
}

func writeCookiesDB(t *testing.T, path, password, token string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	con, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer con.Close()
	_, _ = con.Exec("CREATE TABLE meta (key TEXT, value TEXT)")
	_, _ = con.Exec("INSERT INTO meta VALUES ('version', '24')")
	_, _ = con.Exec("CREATE TABLE cookies (host_key TEXT, name TEXT, value TEXT, encrypted_value BLOB)")
	inner := []byte(token)
	sum := sha256.Sum256([]byte(host))
	inner = append(sum[:], inner...)
	blob := encryptV10(inner, password)
	_, _ = con.Exec("INSERT INTO cookies VALUES (?,?,?,?)", host, "session", "", blob)
}

func fixtureTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writePersistentDB(t, root, fixtureUserID, fixtureName)
	sum := md5.Sum([]byte(fixtureUserID))
	cookies := filepath.Join(root, "aha", "users", hex.EncodeToString(sum[:]), "profile_explorer", "Cookies")
	writeCookiesDB(t, cookies, fixturePassword, fixtureSession)
	return root
}

func TestUserDirHash(t *testing.T) {
	if UserDirHash("10001") != UserDirHash(fixtureUserID) {
		t.Fatal("hash mismatch")
	}
}

func TestDecryptV24Cookie(t *testing.T) {
	sum := sha256.Sum256([]byte(host))
	inner := append(sum[:], []byte(fixtureSession)...)
	blob := encryptV10(inner, fixturePassword)
	got, err := DecryptChromiumValue(blob, fixturePassword, host, 24)
	if err != nil {
		t.Fatal(err)
	}
	if got != fixtureSession {
		t.Fatalf("got %q", got)
	}
}

func TestWrongPasswordFails(t *testing.T) {
	blob := encryptV10([]byte("hello-token-value"), fixturePassword)
	_, err := DecryptChromiumValue(blob, "wrong-password-xx", host, 10)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadSession(t *testing.T) {
	root := fixtureTree(t)
	t.Setenv("LARKDESK_LARKSHELL", root)
	t.Setenv("LARKDESK_COOKIE_PASSWORD", fixturePassword)
	t.Setenv("LARKDESK_TRY_KEYCHAIN", "0")
	auth, err := Load(root, [][2]string{{"env", fixturePassword}})
	if err != nil {
		t.Fatal(err)
	}
	if auth.Identity.Name != fixtureName || auth.Identity.UserID != fixtureUserID {
		t.Fatalf("%+v", auth.Identity)
	}
	if auth.Cookies["session"] != fixtureSession {
		t.Fatalf("cookies %+v", auth.Cookies)
	}
}

func TestMissingDir(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope"), nil)
	if err == nil {
		t.Fatal("expected error")
	}
}
