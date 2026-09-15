package version

import "testing"

func TestCanonical(t *testing.T) {
	cases := []struct{ in, want string }{
		{"1.1.0", "v1.1.0"},
		{"v1.1.0", "v1.1.0"},
		{"dev", "dev"},
		{"", ""},
	}
	for _, c := range cases {
		if got := canonical(c.in); got != c.want {
			t.Fatalf("canonical(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestStringAndInfo(t *testing.T) {
	old := Version
	defer func() { Version = old }()
	Version = "1.2.3"
	if got := String(); got != "v1.2.3" {
		t.Fatalf("String() = %q, want v1.2.3", got)
	}
	if got := Info(); got == "" || got[:20] != "google-calendar-mcp " {
		t.Fatalf("Info() = %q, want it to name the binary", got)
	}
}
