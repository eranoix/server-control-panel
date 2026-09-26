// session_meta.go — session metadata/ACL, tool-agnostic (after the engine
// migration). It replaced the old file, which no longer exists: the functions
// that talked to the previous engine's CLI
// (list/kill/rename/has/capture over the vpsmgr socket) were removed; the engine
// is dtach (backend_dtach.go) behind the Session* dispatchers (session.go). What
// is left here is only the pieces that do not depend on the engine: name
// sanitisation, quota, ownership-filtered listing and the management gate.
package pty

// ownedSessionCount returns how many LIVE sessions the user owns — used to
// enforce the per-user quota (pty.go). It counts only the ones that still have a
// dtach master standing: ownership of a session that died on its own (the shell
// exited → the master exits) is only released on an explicit kill, so dead
// entries piled up and the user hit the cap (maxSessionsPerUser) with ZERO live
// sessions, unable to create any more. Filtering by liveness fixes the lockout
// without touching the ownership map (dead entries are already invisible in the
// picker — SessionListAll only lists live ones).
func ownedSessionCount(own *Ownership, user string) int {
	if own == nil {
		return 0
	}
	n := 0
	for _, name := range own.SessionsOf(user) {
		if alive, _ := SessionHas(name); alive {
			n++
		}
	}
	return n
}

// SessionListForUser returns only the sessions visible to the user under the
// ownership registry. Used by the per-user terminal picker.
//
// Visibility rule (mirrors Ownership.VisibleTo):
//   - a session registered under the user → visible.
//   - an unowned session AND the user is primary → visible (legacy adoption).
//   - otherwise → invisible.
//
// primary = any admin (config.Primary + Admin flags). own may be nil (it degrades
// to "an admin sees everything unowned, nobody else sees anything").
func SessionListForUser(user string, primary bool, own *Ownership) ([]map[string]any, error) {
	all, err := SessionListAll()
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(all))
	for _, s := range all {
		name, _ := s["name"].(string)
		if own.VisibleTo(name, user, primary) {
			out = append(out, s)
		}
	}
	return out, nil
}

// OwnsSession reports whether user may manage sessionName DESTRUCTIVELY
// (kill/rename/detach/backup/scrollback). The handlers reject with a 404 (not a
// 403 — existence never leaks).
//
// It is the MANAGEMENT gate, stricter than VisibleTo (the LISTING gate). An admin
// manages any session; a non-admin only their own. Crucially a non-admin does NOT
// manage an AudienceAll ("*") session even while seeing it in the picker —
// "Everyone" sessions are shared to USE, but only an admin kills or renames one.
// A nil own plus a non-primary user → false (own.Owner tolerates nil).
func OwnsSession(user, sessionName string, primary bool, own *Ownership) bool {
	if user == "" || sessionName == "" {
		return false
	}
	if primary {
		return true
	}
	return own.Owner(sessionName) == user
}

// SessionAccountResolver resolves a session's effective accountID. Implemented
// by *claudeacct.Store (implicit satisfaction). Defined here to avoid an import
// cycle pty → claudeacct.
type SessionAccountResolver interface {
	SessionAccountID(session, consumer string) string
}

// SessionListAnnotated returns every live session annotated with its owner ("assigned") and
// effective Claude account ("claude_account"). The master admin view of the session
// manager — the handler gates on r.isPrimary before calling. accts
// may be nil (claude_account omitted). pane_cmd does not exist in dtach (one
// session = one process), so it stays empty.
func SessionListAnnotated(own *Ownership, accts SessionAccountResolver, consumer string) ([]map[string]any, error) {
	all, err := SessionListAll()
	if err != nil {
		return nil, err
	}
	for _, s := range all {
		name, _ := s["name"].(string)
		s["assigned"] = own.Owner(name)
		if accts != nil {
			s["claude_account"] = accts.SessionAccountID(name, consumer)
		}
	}
	return all, nil
}

// SafeSessionName is the exported wrapper around safeSessionName — callers
// outside the package sanitise names the same way the backend does, so ownership
// keys match.
func SafeSessionName(s string) string { return safeSessionName(s) }

// OwnedSessionCountLive exposes the user's count of LIVE sessions (quota).
func OwnedSessionCountLive(own *Ownership, user string) int { return ownedSessionCount(own, user) }

// MaxSessionsPerUser exports the per-user cap (defined in pty.go).
const MaxSessionsPerUser = maxSessionsPerUser

// safeSessionName keeps only [A-Za-z0-9_-] (max 40 chars). Default "main".
func safeSessionName(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s) && len(out) < 40; i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return "main"
	}
	return string(out)
}
