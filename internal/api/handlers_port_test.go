package api

import "testing"

func TestParsePortPath(t *testing.T) {
	cases := []struct {
		in        string
		port      int
		rest      string
		needSlash bool
		ok        bool
	}{
		{"/_port/8000/", 8000, "/", false, true},
		{"/_port/8000", 8000, "/", true, true},
		{"/_port/5173/foo/bar", 5173, "/foo/bar", false, true},
		{"/_port/3000/a?b=1", 3000, "/a?b=1", false, true},
		{"/_port/", 0, "", false, false},
		{"/_port/abc/", 0, "", false, false},
		{"/_port/0/", 0, "", false, false},
		{"/_port/99999/", 0, "", false, false},
		{"/other/8000/", 0, "", false, false},
	}
	for _, c := range cases {
		port, rest, needSlash, ok := parsePortPath(c.in)
		if ok != c.ok || port != c.port || rest != c.rest || needSlash != c.needSlash {
			t.Errorf("parsePortPath(%q) = (%d,%q,%v,%v), want (%d,%q,%v,%v)",
				c.in, port, rest, needSlash, ok, c.port, c.rest, c.needSlash, c.ok)
		}
	}
}

func TestForwardablePort(t *testing.T) {
	yes := []int{1024, 3000, 5173, 8000, 8091, 65535}
	no := []int{0, 22, 80, 443, 1023, selfPort, 70000, -1}
	for _, p := range yes {
		if !forwardablePort(p) {
			t.Errorf("forwardablePort(%d) = false, want true", p)
		}
	}
	for _, p := range no {
		if forwardablePort(p) {
			t.Errorf("forwardablePort(%d) = true, want false", p)
		}
	}
}
