package jira

import "testing"

// TestAvatarHostAllowed pins the avatar proxy's allowlist (SSRF defence).
func TestAvatarHostAllowed(t *testing.T) {
	const site = "jordan.atlassian.net"
	allow := []string{
		"secure.gravatar.com", "gravatar.com",
		"i0.wp.com", "i1.wp.com",
		"avatar-management--avatars.us-west-2.prod.public.atl-paas.net",
		"jordan.atlassian.net", // the site itself
		"foo.atlassian.net",    // any atlassian.net
	}
	deny := []string{
		"evil.com", "169.254.169.254", "localhost", "127.0.0.1",
		"gravatar.com.evil.com", "atlassian.net.evil.com",
		"", "internal.svc",
	}
	for _, h := range allow {
		if !avatarHostAllowed(h, site) {
			t.Errorf("host %q should be allowed", h)
		}
	}
	for _, h := range deny {
		if avatarHostAllowed(h, site) {
			t.Errorf("host %q should NOT be allowed (SSRF)", h)
		}
	}
}

// TestIsSafeImageType pins the content-type allowlist (defence against XSS by
// content-type smuggling). svg+xml and html MUST be refused — the avatar is
// served from our own origin and SVG/HTML execute script.
func TestIsSafeImageType(t *testing.T) {
	ok := []string{
		"image/png", "image/jpeg", "image/jpg", "image/gif", "image/webp", "image/avif",
		"IMAGE/PNG", "image/png; charset=binary", " image/jpeg ",
	}
	bad := []string{
		"image/svg+xml", "image/svg+xml; charset=utf-8",
		"text/html", "text/html; charset=utf-8",
		"application/xhtml+xml", "application/octet-stream",
		"text/plain", "application/javascript", "",
	}
	for _, ct := range ok {
		if !isSafeImageType(ct) {
			t.Errorf("content-type %q should be accepted", ct)
		}
	}
	for _, ct := range bad {
		if isSafeImageType(ct) {
			t.Errorf("content-type %q should NOT be accepted (XSS)", ct)
		}
	}
}
