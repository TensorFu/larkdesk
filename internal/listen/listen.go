package listen

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode"

	"github.com/TensorFu/larkdesk/internal/contacts"
	"github.com/TensorFu/larkdesk/internal/frontier"
	"github.com/TensorFu/larkdesk/internal/pb"
	"github.com/TensorFu/larkdesk/internal/send"
	"github.com/TensorFu/larkdesk/internal/session"
)

const (
	CmdPushMessages   = 6
	CmdPushMessagesV2 = 5065
	seenMax           = 512
)

type Event struct {
	TS        uint64 `json:"ts"`
	ChatID    string `json:"chat_id"`
	MessageID string `json:"message_id"`
	FromID    string `json:"-"`
	FromName  string `json:"-"`
	Text      string `json:"text"`
	Self      bool   `json:"self"`
	Cmd       uint64 `json:"-"`
}

func (e Event) MarshalJSON() ([]byte, error) {
	frm := map[string]string{"id": e.FromID}
	if e.FromName != "" {
		frm["name"] = e.FromName
	}
	return json.Marshal(map[string]any{
		"ts":         e.TS,
		"chat_id":    e.ChatID,
		"message_id": e.MessageID,
		"from":       frm,
		"text":       e.Text,
		"self":       e.Self,
	})
}

func printable(s string) bool {
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

func ExtractPlainText(buf []byte) string {
	var inner, stringsFound []string
	var walk func([]byte)
	walk = func(raw []byte) {
		fields := pb.DecodeFields(raw)
		tags := map[int]bool{}
		for _, f := range fields {
			tags[f.Num] = true
		}
		for _, f := range fields {
			if f.Wire != "bytes" {
				continue
			}
			if s, ok := utf8str(f.Bytes); ok && printable(s) {
				stringsFound = append(stringsFound, s)
				if f.Num == 2 && tags[3] && strings.TrimSpace(s) != "" && !isDigits(s) {
					inner = append(inner, s)
				}
			}
			if len(f.Bytes) > 0 {
				walk(f.Bytes)
			}
		}
	}
	walk(buf)
	for _, t := range inner {
		if strings.TrimSpace(t) != "" {
			return t
		}
	}
	best := ""
	for _, s := range stringsFound {
		if !isDigits(s) && printable(s) && len(s) > len(best) {
			best = s
		}
	}
	return best
}

func utf8str(b []byte) (string, bool) {
	s := string(b)
	if s == "" {
		return "", false
	}
	return s, true
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

type msg struct {
	ID      string
	Type    uint64
	FromID  string
	TS      uint64
	Content []byte
	ChatID  string
}

func messageFromFields(buf []byte) (msg, bool) {
	fields := pb.DecodeFields(buf)
	if len(fields) == 0 {
		return msg{}, false
	}
	by := map[int][]pb.Field{}
	for _, f := range fields {
		by[f.Num] = append(by[f.Num], f)
	}
	t, ok := by[2]
	if !ok || t[0].Wire != "varint" {
		return msg{}, false
	}
	m := msg{Type: t[0].Int}
	if f, ok := by[1]; ok && f[0].Wire == "bytes" {
		if s, good := pb.UTF8Printable(f[0].Bytes); good {
			m.ID = s
		} else {
			m.ID = string(f[0].Bytes)
		}
	}
	if f, ok := by[3]; ok && f[0].Wire == "bytes" {
		if s, good := pb.UTF8Printable(f[0].Bytes); good {
			m.FromID = s
		}
	}
	if f, ok := by[4]; ok && f[0].Wire == "varint" {
		m.TS = f[0].Int
	}
	if f, ok := by[5]; ok && f[0].Wire == "bytes" {
		m.Content = f[0].Bytes
	}
	if f, ok := by[10]; ok && f[0].Wire == "bytes" {
		if s, good := pb.UTF8Printable(f[0].Bytes); good {
			m.ChatID = s
		}
	}
	return m, true
}

func ParsePushMessages(payload []byte) []msg {
	var out []msg
	for _, f := range pb.DecodeFields(payload) {
		if f.Wire != "bytes" {
			continue
		}
		if direct, ok := messageFromFields(f.Bytes); ok && direct.Type == send.MsgTypeText {
			out = append(out, direct)
			continue
		}
		innerID := ""
		var innerMsg []byte
		for _, inf := range pb.DecodeFields(f.Bytes) {
			if inf.Num == 1 && inf.Wire == "bytes" {
				if s, ok := pb.UTF8Printable(inf.Bytes); ok {
					innerID = s
				}
			} else if inf.Num == 2 && inf.Wire == "bytes" {
				innerMsg = inf.Bytes
			}
		}
		if len(innerMsg) == 0 {
			continue
		}
		parsed, ok := messageFromFields(innerMsg)
		if !ok {
			continue
		}
		if parsed.ID == "" {
			parsed.ID = innerID
		}
		if parsed.Type == send.MsgTypeText {
			out = append(out, parsed)
		}
	}
	return out
}

func EventsFromFrame(raw []byte, selfID string, names map[string]string) []Event {
	fr := frontier.DecodeFrame(raw)
	if fr.Cmd != CmdPushMessages && fr.Cmd != CmdPushMessagesV2 {
		return nil
	}
	if len(fr.Payload) == 0 {
		return nil
	}
	var events []Event
	for _, m := range ParsePushMessages(fr.Payload) {
		text := ExtractPlainText(m.Content)
		if strings.TrimSpace(text) == "" {
			continue
		}
		events = append(events, Event{
			TS:        m.TS,
			ChatID:    m.ChatID,
			MessageID: m.ID,
			FromID:    m.FromID,
			FromName:  names[m.FromID],
			Text:      text,
			Self:      selfID != "" && m.FromID == selfID,
			Cmd:       fr.Cmd,
		})
	}
	return events
}

func nameMap(auth session.Auth) map[string]string {
	names := map[string]string{auth.Identity.UserID: auth.Identity.Name}
	cs, _, err := contacts.List(auth, "")
	if err != nil {
		return names
	}
	for _, c := range cs {
		if c.ID != "" && c.Name != "" {
			if _, ok := names[c.ID]; !ok {
				names[c.ID] = c.Name
			}
		}
	}
	return names
}

func Run(auth session.Auth, fromWho, execCmd string) error {
	if !auth.HasSessionCookie() {
		return fmt.Errorf("no decrypted session cookie")
	}
	allowID := ""
	if strings.TrimSpace(fromWho) != "" {
		p, err := send.ResolvePerson(auth, fromWho)
		if err != nil {
			return err
		}
		allowID = p.ID
	}
	names := nameMap(auth)
	fmt.Fprintf(os.Stderr, "larkdesk: listening as %s\n", auth.Identity.Name)

	seen := []string{}
	seenSet := map[string]bool{}
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)

	for {
		conn, err := frontier.Dial(auth)
		if err != nil {
			time.Sleep(3 * time.Second)
			continue
		}
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				conn.Close()
				time.Sleep(3 * time.Second)
				break
			}
			raw = frontier.MaybeGunzip(raw)
			for _, ev := range EventsFromFrame(raw, auth.Identity.UserID, names) {
				if allowID != "" && ev.FromID != allowID {
					continue
				}
				if ev.MessageID != "" {
					if seenSet[ev.MessageID] {
						continue
					}
					if len(seen) == seenMax {
						delete(seenSet, seen[0])
						seen = seen[1:]
					}
					seen = append(seen, ev.MessageID)
					seenSet[ev.MessageID] = true
				}
				if err := enc.Encode(ev); err != nil {
					return err
				}
				if execCmd != "" {
					b, _ := json.Marshal(ev)
					cmd := exec.Command("sh", "-c", execCmd)
					cmd.Stdin = strings.NewReader(string(b))
					cmd.Stdout = os.Stderr
					cmd.Stderr = os.Stderr
					_ = cmd.Run()
				}
			}
		}
	}
}
