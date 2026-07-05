package proxies

import "testing"

func TestParsePopularFormats(t *testing.T) {
	tests := []struct {
		in, scheme, host, port, user, pass string
	}{
		{"1.2.3.4:8080:u:p", "http", "1.2.3.4", "8080", "u", "p"},
		{"u:p@1.2.3.4:8080", "http", "1.2.3.4", "8080", "u", "p"},
		{"socks5://u:p@1.2.3.4:1080", "socks5", "1.2.3.4", "1080", "u", "p"},
		{"https://1.2.3.4:8443", "https", "1.2.3.4", "8443", "", ""},
	}
	for _, tt := range tests {
		p, err := Parse(tt.in, "http")
		if err != nil {
			t.Fatalf("Parse(%q): %v", tt.in, err)
		}
		if p.Scheme != tt.scheme || p.Host != tt.host || p.Port != tt.port || p.Username != tt.user || p.Password != tt.pass {
			t.Fatalf("Parse(%q) = %+v", tt.in, p)
		}
	}
}
