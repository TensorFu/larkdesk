package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/TensorFu/larkdesk/internal/contacts"
	"github.com/TensorFu/larkdesk/internal/listen"
	"github.com/TensorFu/larkdesk/internal/search"
	"github.com/TensorFu/larkdesk/internal/send"
	"github.com/TensorFu/larkdesk/internal/session"
)

const version = "0.1.0"

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func die(err error) int {
	fmt.Fprintf(os.Stderr, "larkdesk: %v\n", err)
	return 1
}

func usage() {
	fmt.Fprintf(os.Stderr, `larkdesk %s
Reuse the already-logged-in local Lark.app / 飞书 desktop session.

  larkdesk whoami
  larkdesk contacts
  larkdesk search <query>
  larkdesk send <to> <text...> [--dry-run]
  larkdesk listen [--from <name-or-id>] [--exec <cmd>]
`, version)
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		usage()
		return 2
	}
	switch args[0] {
	case "-h", "--help", "help":
		usage()
		return 0
	case "--version", "-v", "version":
		fmt.Printf("larkdesk %s\n", version)
		return 0
	case "whoami":
		auth, err := session.Load("", nil)
		if err != nil {
			return die(err)
		}
		printJSON(map[string]any{
			"ok":        true,
			"name":      auth.Identity.Name,
			"id":        auth.Identity.UserID,
			"tenant":    auth.Identity.TenantName,
			"tenant_id": auth.Identity.TenantID,
			"domain":    auth.Identity.Domain,
			"auth":      "desktop-session",
		})
		return 0
	case "contacts":
		auth, err := session.Load("", nil)
		if err != nil {
			return die(err)
		}
		cs, source, err := contacts.List(auth, "")
		if err != nil {
			return die(err)
		}
		list := make([]map[string]any, 0, len(cs))
		for _, c := range cs {
			list = append(list, c.AsMap())
		}
		printJSON(map[string]any{
			"ok":       true,
			"identity": map[string]any{"name": auth.Identity.Name, "id": auth.Identity.UserID},
			"contacts": list,
			"count":    len(cs),
			"source":   source,
		})
		return 0
	case "search":
		if len(args) < 2 {
			return die(fmt.Errorf("search query required"))
		}
		auth, err := session.Load("", nil)
		if err != nil {
			return die(err)
		}
		hits, err := search.Contacts(auth, strings.Join(args[1:], " "))
		if err != nil {
			return die(err)
		}
		list := make([]map[string]any, 0, len(hits))
		for _, c := range hits {
			list = append(list, c.AsMap())
		}
		printJSON(map[string]any{
			"ok":       true,
			"query":    strings.Join(args[1:], " "),
			"identity": map[string]any{"name": auth.Identity.Name, "id": auth.Identity.UserID},
			"contacts": list,
			"count":    len(hits),
			"source":   "universal_search",
		})
		return 0
	case "send":
		dry := false
		rest := []string{}
		for _, a := range args[1:] {
			if a == "--dry-run" {
				dry = true
				continue
			}
			rest = append(rest, a)
		}
		if len(rest) < 2 {
			return die(fmt.Errorf("send needs a person and text"))
		}
		auth, err := session.Load("", nil)
		if err != nil {
			return die(err)
		}
		out, err := send.Text(auth, rest[0], strings.Join(rest[1:], " "), dry)
		if err != nil {
			return die(err)
		}
		out["ok"] = true
		out["source"] = "put_p2p+put_message"
		printJSON(out)
		return 0
	case "listen":
		fromWho, execCmd := "", ""
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--from":
				i++
				if i >= len(args) {
					return die(fmt.Errorf("--from needs a value"))
				}
				fromWho = args[i]
			case "--exec":
				i++
				if i >= len(args) {
					return die(fmt.Errorf("--exec needs a value"))
				}
				execCmd = args[i]
			default:
				return die(fmt.Errorf("unknown listen flag %s", args[i]))
			}
		}
		auth, err := session.Load("", nil)
		if err != nil {
			return die(err)
		}
		if err := listen.Run(auth, fromWho, execCmd); err != nil {
			return die(err)
		}
		return 0
	default:
		return die(fmt.Errorf("unknown command %s", args[0]))
	}
}
