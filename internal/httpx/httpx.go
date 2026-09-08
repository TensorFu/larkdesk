package httpx

import (
	"net/http"
	"net/url"
	"os"
	"time"
)

func ProxyURL() *url.URL {
	if v, ok := os.LookupEnv("LARKDESK_PROXY"); ok {
		if v == "" {
			return nil
		}
		u, err := url.Parse(v)
		if err != nil {
			return nil
		}
		return u
	}
	for _, k := range []string{"HTTPS_PROXY", "https_proxy"} {
		if v := os.Getenv(k); v != "" {
			u, err := url.Parse(v)
			if err == nil {
				return u
			}
		}
	}
	u, _ := url.Parse("http://127.0.0.1:1082")
	return u
}

func Client(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if p := ProxyURL(); p != nil {
		transport.Proxy = http.ProxyURL(p)
	} else {
		transport.Proxy = nil
	}
	return &http.Client{Timeout: timeout, Transport: transport}
}
