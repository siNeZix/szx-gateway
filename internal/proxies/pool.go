package proxies

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	xnetproxy "golang.org/x/net/proxy"

	"szx-gateway/internal/store"
)

type Pool struct {
	store *store.Store
	mu    sync.Mutex
	next  int
}

func NewPool(s *store.Store) *Pool {
	return &Pool{store: s}
}

func Parse(raw, defaultScheme string) (store.DBProxy, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return store.DBProxy{}, errors.New("empty proxy")
	}
	if defaultScheme == "" {
		defaultScheme = "http"
	}
	defaultScheme = strings.ToLower(defaultScheme)
	if defaultScheme != "http" && defaultScheme != "https" && defaultScheme != "socks5" {
		return store.DBProxy{}, fmt.Errorf("unsupported scheme %q", defaultScheme)
	}

	if strings.Contains(s, "://") {
		return parseURL(s)
	}
	if strings.Contains(s, "@") {
		return parseURL(defaultScheme + "://" + s)
	}

	parts := strings.Split(s, ":")
	if len(parts) != 2 && len(parts) != 4 {
		return store.DBProxy{}, fmt.Errorf("unsupported proxy format")
	}
	p := store.DBProxy{Raw: s, Scheme: defaultScheme, Host: strings.TrimSpace(parts[0]), Port: strings.TrimSpace(parts[1]), Status: "unchecked"}
	if len(parts) == 4 {
		p.Username = strings.TrimSpace(parts[2])
		p.Password = strings.TrimSpace(parts[3])
	}
	return validate(p)
}

func parseURL(raw string) (store.DBProxy, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return store.DBProxy{}, err
	}
	p := store.DBProxy{Raw: raw, Scheme: strings.ToLower(u.Scheme), Host: u.Hostname(), Port: u.Port(), Status: "unchecked"}
	if u.User != nil {
		p.Username = u.User.Username()
		p.Password, _ = u.User.Password()
	}
	return validate(p)
}

func validate(p store.DBProxy) (store.DBProxy, error) {
	if p.Scheme != "http" && p.Scheme != "https" && p.Scheme != "socks5" {
		return p, fmt.Errorf("unsupported scheme %q", p.Scheme)
	}
	if p.Host == "" || p.Port == "" {
		return p, errors.New("missing host or port")
	}
	port, err := strconv.Atoi(p.Port)
	if err != nil || port < 1 || port > 65535 {
		return p, fmt.Errorf("invalid port %q", p.Port)
	}
	return p, nil
}

func proxyURL(p store.DBProxy) *url.URL {
	u := &url.URL{Scheme: p.Scheme, Host: net.JoinHostPort(p.Host, p.Port)}
	if p.Username != "" {
		u.User = url.UserPassword(p.Username, p.Password)
	}
	return u
}

func (p *Pool) Client(useProxy bool, timeout time.Duration) (*http.Client, int64) {
	if !useProxy {
		return &http.Client{Timeout: timeout}, 0
	}
	proxy, ok := p.Next()
	if !ok {
		return &http.Client{Timeout: timeout}, 0
	}
	return clientFor(proxy, timeout), proxy.ID
}

func clientFor(proxy store.DBProxy, timeout time.Duration) *http.Client {
	tr := &http.Transport{}
	if proxy.Scheme == "socks5" {
		dialer, err := xnetproxy.SOCKS5("tcp", net.JoinHostPort(proxy.Host, proxy.Port), auth(proxy), xnetproxy.Direct)
		if err == nil {
			tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialer.Dial(network, addr)
			}
		}
	} else {
		u := proxyURL(proxy)
		tr.Proxy = http.ProxyURL(u)
	}
	return &http.Client{Timeout: timeout, Transport: tr}
}

func auth(p store.DBProxy) *xnetproxy.Auth {
	if p.Username == "" {
		return nil
	}
	return &xnetproxy.Auth{User: p.Username, Password: p.Password}
}

func (p *Pool) Next() (store.DBProxy, bool) {
	items, err := p.store.GetProxies(true)
	if err != nil || len(items) == 0 {
		return store.DBProxy{}, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	item := items[p.next%len(items)]
	p.next++
	return item, true
}

func (p *Pool) Check(proxy store.DBProxy) (string, string) {
	client := clientFor(proxy, 5*time.Second)
	req, err := http.NewRequest(http.MethodHead, "https://openrouter.ai/", nil)
	if err != nil {
		return "invalid", err.Error()
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Range", "bytes=0-0")
	resp, err := client.Do(req)
	if err != nil {
		return "invalid", err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 500 {
		return "active", ""
	}
	return "invalid", resp.Status
}

func (p *Pool) Add(raw []string, defaultScheme string) (int, int, error) {
	added, checked := 0, 0
	seen := map[string]bool{}
	for _, line := range raw {
		px, err := Parse(line, defaultScheme)
		if err != nil {
			continue
		}
		key := px.Scheme + ":" + px.Host + ":" + px.Port + ":" + px.Username + ":" + px.Password
		if seen[key] {
			continue
		}
		seen[key] = true
		px.Status, px.LastError = p.Check(px)
		px.LastCheckedAt = time.Now().UTC()
		ok, err := p.store.AddProxy(px)
		if err != nil {
			return added, checked, err
		}
		checked++
		if ok {
			added++
		}
	}
	return added, checked, nil
}

func (p *Pool) Recheck(ids []int64) error {
	items, err := p.store.GetProxies(false)
	if err != nil {
		return err
	}
	want := map[int64]bool{}
	for _, id := range ids {
		want[id] = true
	}
	for _, px := range items {
		if len(want) > 0 && !want[px.ID] {
			continue
		}
		status, msg := p.Check(px)
		if err := p.store.UpdateProxyCheck(px.ID, status, msg, time.Now()); err != nil {
			return err
		}
	}
	return nil
}

func ShouldUse(settings store.ProxySettings, after429 bool) bool {
	return settings.Mode == "always" || (settings.Mode == "after_429" && after429)
}
