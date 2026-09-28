package scope

import (
	"errors"
	"path/filepath"
	"strings"
)

type User string

const (
	MaxUsernameLen = 40
)

var (
	ErrEmpty       = errors.New("scope: empty username")
	ErrTooLong     = errors.New("scope: username too long")
	ErrInvalidChar = errors.New("scope: invalid character in username")
	ErrReserved    = errors.New("scope: reserved username")
)

var reserved = map[string]struct{}{
	"system":  {},
	"anon":    {},
	"archive": {},
	"root":    {},
	"":        {},
}

func New(raw string) (User, error) {
	if raw == "" {
		return "", ErrEmpty
	}
	if len(raw) > MaxUsernameLen {
		return "", ErrTooLong
	}
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		case c == '_' || c == '-':
		default:
			return "", ErrInvalidChar
		}
	}
	if _, ok := reserved[strings.ToLower(raw)]; ok {
		return "", ErrReserved
	}
	return User(raw), nil
}

func MustNew(raw string) User {
	u, err := New(raw)
	if err != nil {
		panic("scope.MustNew(" + raw + "): " + err.Error())
	}
	return u
}

func (u User) String() string { return string(u) }

func (u User) Valid() bool {
	_, err := New(string(u))
	return err == nil
}

type Paths struct {
	Root string

	Whatsapp string

	WhatsappContainer string

	WhatsappMedia string

	Uploads string

	Browser string
}

func PathsFor(dataDir string, u User) Paths {
	root := filepath.Join(dataDir, "users", u.String())
	wac := filepath.Join("/var/lib/panel-whatsapp", u.String())
	return Paths{
		Root:              root,
		Whatsapp:          filepath.Join(root, "whatsapp"),
		WhatsappContainer: wac,
		WhatsappMedia:     filepath.Join(wac, "media"),
		Uploads:           filepath.Join(root, "uploads"),
		Browser:           filepath.Join(root, "browser"),
	}
}

type Scope struct {
	User      User
	DataDir   string
	Vault     *UserVault
	Paths     Paths
	IsPrimary bool
}
