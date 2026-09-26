package git

import "testing"

func TestParseGitHubToken(t *testing.T) {
	cases := map[string]string{
		"https://northwind-dev:ghp_ABC123@github.com\n":   "ghp_ABC123",
		"https://x-access-token:github_pat_XY@github.com": "github_pat_XY",
		"https://ghp_ONLYTOKEN@github.com":                "ghp_ONLYTOKEN",
		"ghp_BareClassicToken123":                         "ghp_BareClassicToken123",
		"github_pat_11ABCDEFG_xyz":                        "github_pat_11ABCDEFG_xyz",
		"justasingletokenvalue":                           "justasingletokenvalue",
		"":                                                "",
		"   ":                                             "",
		"https://northwind-dev:tok@gitlab.com\n":          "", // não-github → não casa credLineRe
	}
	for in, want := range cases {
		if got := parseGitHubToken(in); got != want {
			t.Errorf("parseGitHubToken(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGitHubOwnerRepo(t *testing.T) {
	cases := []struct {
		url, owner, repo string
		ok               bool
	}{
		{"git@github.com:northwind-dev/vps-manager.git", "northwind-dev", "vps-manager", true},
		{"https://github.com/NorthwindLabs/northwind-web.git", "NorthwindLabs", "northwind-web", true},
		{"https://github.com/NorthwindLabs/northwind-web", "NorthwindLabs", "northwind-web", true},
		{"git@gitlab.com:grp/proj.git", "", "", false},
		{"https://bitbucket.org/team/repo.git", "", "", false},
	}
	for _, c := range cases {
		o, n, ok := githubOwnerRepo(c.url)
		if ok != c.ok || o != c.owner || n != c.repo {
			t.Errorf("githubOwnerRepo(%q) = %q,%q,%v want %q,%q,%v", c.url, o, n, ok, c.owner, c.repo, c.ok)
		}
	}
}
