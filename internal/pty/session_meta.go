package pty

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

func OwnsSession(user, sessionName string, primary bool, own *Ownership) bool {
	if user == "" || sessionName == "" {
		return false
	}
	if primary {
		return true
	}
	return own.Owner(sessionName) == user
}

type SessionAccountResolver interface {
	SessionAccountID(session, consumer string) string
}

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

func SafeSessionName(s string) string { return safeSessionName(s) }

func OwnedSessionCountLive(own *Ownership, user string) int { return ownedSessionCount(own, user) }

const MaxSessionsPerUser = maxSessionsPerUser

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
