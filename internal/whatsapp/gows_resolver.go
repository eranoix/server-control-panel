package whatsapp

import (
	"bufio"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// gows_resolver.go: read-only access to Whatsmeow's internal SQLite (WAHA's
// GOWS engine) to resolve contact names, groups, the @lid -> phone mapping,
// and the flags (archived/pinned/muted).
//
// Why? WAHA Core does not expose this data over REST. But Whatsmeow keeps all
// of it locally in /var/lib/vpsm-whatsapp/sessions/gows/default/gows.db.
// We read it through the `sqlite3` CLI (read-only, mode=ro) — no new Go
// dependency, and no conflict with WAHA writing to the same file.
//
// Tables used:
//
//	whatsmeow_contacts        their_jid (@s.whatsapp.net), first_name,
//	                          full_name, push_name, business_name
//	gows_groups               id (@g.us), name
//	whatsmeow_lid_map         lid (raw id), pn (phone number)
//	whatsmeow_chat_settings   chat_jid, archived, pinned, muted_until

// gowsSnapshot is the result of one full read of the GOWS DB.
type gowsSnapshot struct {
	// contacts: JID @c.us (normalised) -> resolved name (full > first > business > push)
	contacts map[string]string
	// groups: JID @g.us -> the group's name
	groups map[string]string
	// lidToPN: "100000000000003@lid" -> "5522999@c.us" (already normalised and @-suffixed)
	lidToPN map[string]string
	// settings: chat_jid -> flags
	settings map[string]chatFlags
}

type chatFlags struct {
	archived bool
	pinned   bool
	muted    bool
}

// readGOWS takes a full snapshot of the DB. Every read error is silently
// swallowed (a partial snapshot beats nothing). Heavy by design — called once
// per poll cycle (30s). An empty `dbPath` uses the legacy default (the
// gowsDBPath const); in multi-tenant mode the Service passes the per-user path.
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

	// ---- contacts ----
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
			// Priority: full_name > first_name > business_name > push_name.
			// The same order WhatsApp Web uses.
			name := firstNonEmpty(p[1], p[2], p[3], p[4])
			if name != "" {
				snap.contacts[jid] = name
			}
		}
	}

	// ---- groups ----
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

	// ---- lid → pn ----
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
			// The lid in the DB has no suffix; add one so it matches chat_jid.
			lidJID := lidRaw + "@lid"
			pnJID := pn + "@c.us"
			snap.lidToPN[lidJID] = pnJID
		}
	}

	// ---- chat settings (archived/pinned/muted) ----
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

// resolveName returns the best known name for a JID, using:
//
//   - groups             ->  for @g.us
//   - contacts           ->  for @c.us
//   - lidToPN+contacts   ->  for @lid (resolved through the mapping to a contact)
//
// Returns ("", false) when there is nothing.
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
		// Try mapping it to a phone number -> contact.
		if pnJID, ok := g.lidToPN[jid]; ok {
			if n, ok := g.contacts[pnJID]; ok {
				return n, true
			}
		}
		return "", false
	}
	// @c.us (or anything else — a simple fallback)
	if n, ok := g.contacts[jid]; ok {
		return n, true
	}
	return "", false
}

// canonicalJID returns the consolidated JID for a chat — when it is a @lid
// with a mapping to a @c.us, it returns the @c.us form so both become the
// same chat. Otherwise it returns the original JID, normalised.
func (g *gowsSnapshot) canonicalJID(jid string) string {
	jid = normalizeJID(jid)
	if strings.HasSuffix(jid, "@lid") {
		if pnJID, ok := g.lidToPN[jid]; ok {
			return pnJID
		}
	}
	return jid
}

// firstNonEmpty returns the first non-empty string.
func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		s = strings.TrimSpace(s)
		if s != "" {
			return s
		}
	}
	return ""
}
