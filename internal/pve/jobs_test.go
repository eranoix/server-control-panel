package pve

import "testing"

func TestJobScheduled(t *testing.T) {
	one, zero := 1, 0
	cases := []struct {
		name string
		j    BackupJob
		want bool
	}{
		{"explicitly on", BackupJob{Enabled: &one, Schedule: "03:30"}, true},
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
