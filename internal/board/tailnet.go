package board

import (
	"encoding/json"
	"maps"
	"net"
	"net/url"
	"slices"
	"strings"
)

type PhoneLink struct {
	URL, Problem, Port       string
	HTTPS, Funnel, Tailscale bool
	Phones                   int
}

func (l PhoneLink) Ready() string {
	if l.Funnel {
		return ""
	}
	return l.URL
}

const ServeAbsent = "tailscale serve does not proxy this Board yet"

type serveConfig struct {
	Web map[string]struct {
		Handlers map[string]struct{ Proxy string }
	}
	AllowFunnel map[string]bool
}

func ServeLink(status []byte, addr string) (link string, funnel bool, problem string) {
	var cfg serveConfig
	if err := json.Unmarshal(status, &cfg); err != nil {
		return "", false, "tailscale serve status --json did not print a serve config"
	}
	host, port, _ := net.SplitHostPort(addr)
	var mount string
	for _, hostport := range slices.Sorted(maps.Keys(cfg.Web)) {
		handlers := cfg.Web[hostport].Handlers
		for _, path := range slices.Sorted(maps.Keys(handlers)) {
			if !proxiesTo(handlers[path].Proxy, host, port) {
				continue
			}
			funnel = funnel || cfg.AllowFunnel[hostport]
			if path != "/" {
				mount = path
			} else if link == "" {
				link = httpsName(hostport)
			}
		}
	}
	switch {
	case link != "":
		return link, funnel, ""
	case mount != "":
		return "", funnel, "tailscale serve proxies this Board under " + mount + "; the Board needs the root"
	}
	return "", false, ServeAbsent
}

func proxiesTo(proxy, host, port string) bool {
	u, err := url.Parse(proxy)
	if err != nil || u.Scheme != "http" || (u.Path != "" && u.Path != "/") || u.User != nil || u.Port() != port {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if u.Hostname() == "localhost" {
		return ip.IsLoopback()
	}
	return ip.Equal(net.ParseIP(u.Hostname()))
}

func httpsName(hostport string) string {
	name, port, err := net.SplitHostPort(hostport)
	if err != nil || name == "" {
		return ""
	}
	if port == "443" {
		return "https://" + name
	}
	return "https://" + hostport
}

func TailnetStatus(status []byte) (https bool, phones int) {
	var s struct {
		CertDomains []string
		Peer        map[string]struct {
			OS     string
			Online bool
			Tags   []string
		}
	}
	if json.Unmarshal(status, &s) != nil {
		return false, 0
	}
	for _, p := range s.Peer {
		if p.Online && len(p.Tags) == 0 && (strings.EqualFold(p.OS, "android") || strings.EqualFold(p.OS, "ios")) {
			phones++
		}
	}
	return len(s.CertDomains) > 0, phones
}
