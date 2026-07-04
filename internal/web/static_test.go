package web

import "testing"

func TestCleanSPAPath(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"/spa/", "index.html", true},
		{"/spa/keys", "keys", true},
		{"/spa/assets/app.js", "assets/app.js", true},
		{"/spa//models", "models", true},
		{"/spa/../secret", "", false},
	}

	for _, tt := range tests {
		got, ok := cleanSPAPath(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Fatalf("cleanSPAPath(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}
