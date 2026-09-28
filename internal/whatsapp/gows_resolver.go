package whatsapp

import (
	"bufio"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type gowsSnapshot struct {
	contacts map[string]string
	groups   map[string]string
	lidToPN  map[string]string
	settings map[string]chatFlags
}

type chatFlags struct {
	archived bool
	pinned   bool
	muted    bool
}

func readGOWS(dbPath string) *gowsSnapshot {
	snap := &gowsSnapshot{
		contacts: map[string]string{},
		groups:   map[string]string{},
		lidToPN:  map[string]string{},
		settings: map[string]chatFlags{},
	}
	if _, err := exec.LookPath("sqlite3"); err != nil {
		return snap
	}
	if dbPath == "" {
		dbPath = gowsDBPath
	}
	uri := "file:" + dbPath + "?mode=ro&immutable=0"

	out, err := exec.Command("sqlite3", "-readonly", "-separator", "\t", uri, `
		SELECT their_jid, COALESCE(full_name,''), COALESCE(first_name,''),
		       COALESCE(business_name,''), COALESCE(push_name,'')
		FROM whatsmeow_contacts;
	`).Output()
	if err == nil {
		sc := bufio.NewScanner(strings.NewReader(string(out)))
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			p := strings.Split(sc.Text(), "\t")
			if len(p) < 5 {
				continue
			}
			jid := normalizeJID(strings.TrimSpace(p[0]))
			if jid == "" {
				continue
			}
			name := firstNonEmpty(p[1], p[2], p[3], p[4])
			if name != "" {
				snap.contacts[jid] = name
			}
		}
	}

	out, err = exec.Command("sqlite3", "-readonly", "-separator", "\t", uri,
		"SELECT id, name FROM gows_groups WHERE name != '';").Output()
	if err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			p := strings.SplitN(line, "\t", 2)
			if len(p) < 2 {
				continue
			}
			jid := strings.TrimSpace(p[0])
			if !strings.HasSuffix(jid, "@g.us") {
				jid += "@g.us"
			}
			snap.groups[jid] = p[1]
		}
	}

	out, err = exec.Command("sqlite3", "-readonly", "-separator", "\t", uri,
		"SELECT lid, pn FROM whatsmeow_lid_map;").Output()
	if err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			p := strings.SplitN(line, "\t", 2)
			if len(p) < 2 {
				continue
			}
			lidRaw := strings.TrimSpace(p[0])
			pn := strings.TrimSpace(p[1])
			if lidRaw == "" || pn == "" {
				continue
			}
			lidJID := lidRaw + "@lid"
			pnJID := pn + "@c.us"
			snap.lidToPN[lidJID] = pnJID
		}
	}

	out, err = exec.Command("sqlite3", "-readonly", "-separator", "\t", uri,
		"SELECT chat_jid, archived, pinned, muted_until FROM whatsmeow_chat_settings;").Output()
	if err == nil {
		now := time.Now().Unix()
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			p := strings.Split(line, "\t")
			if len(p) < 4 {
				continue
			}
			jid := normalizeJID(strings.TrimSpace(p[0]))
			if jid == "" {
				continue
			}
			mu, _ := strconv.ParseInt(p[3], 10, 64)
			snap.settings[jid] = chatFlags{
				archived: p[1] == "1",
				pinned:   p[2] == "1",
				muted:    mu == -1 || mu > now,
			}
		}
	}
	return snap
}

func (g *gowsSnapshot) resolveName(jid string) (string, bool) {
	if jid == "" {
		return "", false
	}
	if strings.HasSuffix(jid, "@g.us") {
		if n, ok := g.groups[jid]; ok && n != "" {
			return n, true
		}
		return "", false
	}
	if strings.HasSuffix(jid, "@lid") {
		if pnJID, ok := g.lidToPN[jid]; ok {
			if n, ok := g.contacts[pnJID]; ok {
				return n, true
			}
		}
		return "", false
	}
	if n, ok := g.contacts[jid]; ok {
		return n, true
	}
	return "", false
}

func (g *gowsSnapshot) canonicalJID(jid string) string {
	jid = normalizeJID(jid)
	if strings.HasSuffix(jid, "@lid") {
		if pnJID, ok := g.lidToPN[jid]; ok {
			return pnJID
		}
	}
	return jid
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		s = strings.TrimSpace(s)
		if s != "" {
			return s
		}
	}
	return ""
}
