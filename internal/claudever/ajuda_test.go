package claudever

import (
	"os"
	"strconv"
	"time"
)

// Small helpers so the tests read better — what matters in them is the rule
// being checked, not the ritual of building files.
func itoa(i int) string              { return strconv.Itoa(i) }
func mkdirAll(p string) error        { return os.MkdirAll(p, 0o755) }
func writeFile(p, s string) error    { return os.WriteFile(p, []byte(s), 0o644) }
func timeoutCurto() <-chan time.Time { return time.After(2 * time.Second) }
