package web

import "testing"

func TestCleanSPAPath(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"/", "index.html", true},
		{"/keys", "keys", true},
		{"/assets/app.js", "assets/app.js", true},
		{"//models", "models", true},
		{"/../secret", "", false},
	}

	for _, tt := range tests {
		got, ok := cleanSPAPath(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Fatalf("cleanSPAPath(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}
