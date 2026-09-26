package pve

import "testing"

// 🔴 An ABSENT `enabled` means ON in the hypervisor: the field only shows up
// when someone turns the job off. Treating absence as off would make the screen
// call "disarmed" a layer that runs every day — and a screen that says there is
// no backup when there is is the most expensive lie it can tell.
func TestJobAgendado(t *testing.T) {
	um, zero := 1, 0
	casos := []struct {
		nome string
		j    JobDeBackup
		quer bool
	}{
		{"ligado explicitamente", JobDeBackup{Enabled: &um, Schedule: "03:30"}, true},
		// 🔴 The case the first version of this test GOT WRONG: I wrote in the comment
		// that absent means on and then asserted `false` in the table, contradicting
		// myself. Absent is ON (Backup.pm:132-137).
		{"enabled AUSENTE é ligado (default => 1)", JobDeBackup{Schedule: "03:30"}, true},
		{"desligado explicitamente", JobDeBackup{Enabled: &zero, Schedule: "03:30"}, false},
		{"ligado mas sem horário não dispara", JobDeBackup{Enabled: &um}, false},
		{"ausente e sem horário também não", JobDeBackup{}, false},
	}
	for _, c := range casos {
		if got := c.j.Agendado(); got != c.quer {
			t.Errorf("%s: Agendado() = %v, want %v", c.nome, got, c.quer)
		}
	}
}
