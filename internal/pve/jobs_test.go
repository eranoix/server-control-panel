package pve

import "testing"

// 🔴 An ABSENT `enabled` means ON in the hypervisor: the field only shows up
// when someone turns the job off. Treating absence as off would make the screen
// call "disarmed" a layer that runs every day — and a screen that says there is
// no backup when there is is the most expensive lie it can tell.
func TestJobScheduled(t *testing.T) {
	one, zero := 1, 0
	cases := []struct {
		name string
		j    BackupJob
		want bool
	}{
		{"explicitly on", BackupJob{Enabled: &one, Schedule: "03:30"}, true},
		// 🔴 The case the first version of this test GOT WRONG: I wrote in the comment
		// that absent means on and then asserted `false` in the table, contradicting
		// myself. Absent is ON (Backup.pm:132-137).
		{"MISSING enabled is on (default => 1)", BackupJob{Schedule: "03:30"}, true},
		{"explicitly off", BackupJob{Enabled: &zero, Schedule: "03:30"}, false},
		{"on but with no schedule does not fire", BackupJob{Enabled: &one}, false},
		{"missing and with no schedule does not either", BackupJob{}, false},
	}
	for _, c := range cases {
		if got := c.j.IsScheduled(); got != c.want {
			t.Errorf("%s: IsScheduled() = %v, want %v", c.name, got, c.want)
		}
	}
}
