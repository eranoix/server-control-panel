package procs

import (
	"context"
	"os"
	"syscall"
	"testing"
)

func TestSignalByName(t *testing.T) {
	cases := []struct {
		in   string
		want syscall.Signal
		err  bool
	}{
		{"", syscall.SIGTERM, false},
		{"TERM", syscall.SIGTERM, false},
		{"sigterm", syscall.SIGTERM, false},
		{"KILL", syscall.SIGKILL, false},
		{"HUP", syscall.SIGHUP, false},
		{"STOP", syscall.SIGSTOP, false},
		{"CONT", syscall.SIGCONT, false},
		{"INT", syscall.SIGINT, false},
		{"USR1", syscall.SIGUSR1, false},
		{"USR2", syscall.SIGUSR2, false},
		{"BOGUS", 0, true},
	}
	for _, c := range cases {
		got, err := SignalByName(c.in)
		if (err != nil) != c.err {
			t.Errorf("SignalByName(%q) err=%v want err=%v", c.in, err, c.err)
		}
		if !c.err && got != c.want {
			t.Errorf("SignalByName(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestIsDeniedPID1(t *testing.T) {
	if !IsDenied(context.Background(), 1) {
		t.Error("PID 1 must be denied")
	}
	if !IsDenied(context.Background(), 0) {
		t.Error("PID 0 must be denied")
	}
	if !IsDenied(context.Background(), -5) {
		t.Error("negative PID must be denied")
	}
}

func TestIsDeniedSelf(t *testing.T) {
	if !IsDenied(context.Background(), int32(os.Getpid())) {
		t.Error("self PID must be denied")
	}
}

func TestIsDeniedUnknownPID(t *testing.T) {
	// 2^30 is essentially never a valid PID on linux
	if !IsDenied(context.Background(), 1<<30) {
		t.Error("unknown PID should fall closed (deny)")
	}
}

func TestSignalDenied(t *testing.T) {
	err := Signal(context.Background(), 1, syscall.SIGTERM)
	if err != ErrDenied {
		t.Errorf("Signal(pid=1) err=%v, want ErrDenied", err)
	}
}

func TestListBasic(t *testing.T) {
	infos, total, err := List(context.Background(), Filter{}, SortCPU, 5, 0)
	if err != nil {
		t.Fatalf("List err: %v", err)
	}
	if total == 0 || len(infos) == 0 {
		t.Fatalf("List returned empty; total=%d len=%d", total, len(infos))
	}
	if len(infos) > 5 {
		t.Errorf("List limit ignored: got %d, want <=5", len(infos))
	}
	// CPU-sorted: each successive value <= previous
	for i := 1; i < len(infos); i++ {
		if infos[i].CPU > infos[i-1].CPU {
			t.Errorf("not CPU-sorted at index %d: %v > %v", i, infos[i].CPU, infos[i-1].CPU)
		}
	}
}

func TestListFilterByUser(t *testing.T) {
	// At minimum the test binary itself runs under some user; filter by it
	// and we should get back at least our PID.
	me, err := os.Hostname() // any non-empty string is fine; just to ensure cfg loaded
	_ = me
	_ = err
	uid := os.Getenv("USER")
	if uid == "" {
		t.Skip("USER env unset")
	}
	infos, _, err := List(context.Background(), Filter{User: uid}, SortCPU, 0, 0)
	if err != nil {
		t.Fatalf("List err: %v", err)
	}
	for _, i := range infos {
		if i.User == "" {
			continue // some kernel threads have no user
		}
		if i.User != uid {
			t.Errorf("filter leaked: got user %q in result for filter %q", i.User, uid)
		}
	}
}

func TestSanitizeStripsControl(t *testing.T) {
	in := "foo\x00bar\x01baz\nqux"
	got := sanitize(in)
	for _, r := range got {
		if r < 0x20 && r != '\t' {
			t.Errorf("sanitize left control byte %x in %q", r, got)
		}
	}
}
