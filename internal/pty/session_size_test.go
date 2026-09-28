package pty

import "testing"

func TestPtyTakesSmallestClientSize(t *testing.T) {
	s := &sharedLog{}

	cols, rows, changed, _ := s.registerSize(1, 120, 40, false)
	if !changed || cols != 120 || rows != 40 {
		t.Fatalf("first client: %dx%d changed=%v; wanted 120x40 changed=true", cols, rows, changed)
	}

	cols, rows, changed, _ = s.registerSize(2, 67, 53, false)
	if !changed || cols != 67 || rows != 40 {
		t.Fatalf("with two clients: %dx%d changed=%v; wanted 67x40 (smaller of each axis)", cols, rows, changed)
	}
}

func TestAllClientsNotifiedWhenSizeChanges(t *testing.T) {
	s := &sharedLog{}
	var notifiedA, notifiedB [2]uint16
	s.registerSize(1, 120, 40, false)
	s.registerSize(2, 90, 50, false)
	s.registerApplier(1, func(c, r uint16) { notifiedA = [2]uint16{c, r} })
	s.registerApplier(2, func(c, r uint16) { notifiedB = [2]uint16{c, r} })

	_, _, changed, warnings := s.registerSize(3, 67, 53, false)
	if !changed {
		t.Fatal("the arrival of a smaller client did not change the size")
	}
	if len(warnings) != 2 {
		t.Fatalf("%d notices; wanted 2 — whoever was already attached has to know", len(warnings))
	}
	for _, a := range warnings {
		a(67, 40)
	}
	if notifiedA != [2]uint16{67, 40} || notifiedB != [2]uint16{67, 40} {
		t.Errorf("notices = %v and %v; wanted 67x40 on both", notifiedA, notifiedB)
	}
}

func TestNewcomerReceivesCurrentSize(t *testing.T) {
	s := &sharedLog{}
	s.registerSize(1, 67, 53, false)

	cols, rows := s.registerApplier(2, func(uint16, uint16) {})
	if cols != 67 || rows != 53 {
		t.Errorf("registerApplier returned %dx%d; wanted what is already in effect, 67x53", cols, rows)
	}
}

func TestReassertingSameSizeLeavesPtyAlone(t *testing.T) {
	s := &sharedLog{}
	s.registerSize(1, 80, 24, false)

	for i := 0; i < 5; i++ {
		if _, _, changed, _ := s.registerSize(1, 80, 24, false); changed {
			t.Fatalf("reassertion %d became SIGWINCH", i+1)
		}
	}
}

func TestDepartingClientStopsShrinkingSession(t *testing.T) {
	s := &sharedLog{}
	s.registerSize(1, 120, 40, false)
	s.registerSize(2, 67, 53, false)

	cols, rows, changed, _ := s.forgetSize(2)
	if !changed || cols != 120 || rows != 40 {
		t.Fatalf("after the exit: %dx%d changed=%v; wanted 120x40", cols, rows, changed)
	}
}

func TestNoClientsKeepsSize(t *testing.T) {
	s := &sharedLog{}
	s.registerSize(1, 80, 24, false)

	cols, rows, changed, _ := s.forgetSize(1)
	if changed {
		t.Error("touched the PTY with nobody attached")
	}
	if cols != 80 || rows != 24 {
		t.Errorf("returned %dx%d; wanted to keep 80x24", cols, rows)
	}
}

func TestDegenerateSizeDoesNotDragSession(t *testing.T) {
	s := &sharedLog{}
	s.registerSize(1, 80, 24, false)

	for _, d := range []struct{ c, r uint16 }{{1, 1}, {0, 0}, {80, 0}, {2000, 24}, {80, 2000}} {
		if _, _, changed, _ := s.registerSize(2, d.c, d.r, false); changed {
			t.Errorf("%dx%d entered the calculation", d.c, d.r)
		}
	}
	if s.appliedCols != 80 || s.appliedRows != 24 {
		t.Errorf("the session was dragged to %dx%d", s.appliedCols, s.appliedRows)
	}
}
