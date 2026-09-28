package webassets

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"path"
	"strings"
	"sync"
)

const stampPrefix = "//# panel-src-sha256="

const appDir = "vendor/panel/app"

var validMinified = sync.OnceValue(func() map[string]string {
	valid := map[string]string{}
	sub := SubFS()
	entries, err := fs.ReadDir(sub, appDir)
	if err != nil {
		return valid
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".js") || strings.HasSuffix(name, ".min.js") {
			continue
		}
		source := path.Join(appDir, name)
		min := path.Join(appDir, strings.TrimSuffix(name, ".js")+".min.js")
		if verify(sub, source, min) {
			valid[source] = min
		}
	}
	return valid
})

func verify(sub fs.FS, source, min string) bool {
	bMin, err := fs.ReadFile(sub, min)
	if err != nil {
		return false
	}
	stamp, ok := readStamp(bMin)
	if !ok {
		return false
	}
	bSource, err := fs.ReadFile(sub, source)
	if err != nil {
		return false
	}
	sum := sha256.Sum256(bSource)
	return stamp == hex.EncodeToString(sum[:])
}

func readStamp(b []byte) (string, bool) {
	lines := bytes.Split(b, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 {
			continue
		}
		if !bytes.HasPrefix(line, []byte(stampPrefix)) {
			return "", false
		}
		hash := string(bytes.TrimPrefix(line, []byte(stampPrefix)))
		if len(hash) != hex.EncodedLen(sha256.Size) {
			return "", false
		}
		if _, err := hex.DecodeString(hash); err != nil {
			return "", false
		}
		return hash, true
	}
	return "", false
}

func MinifiedOf(source string) (string, bool) {
	min, ok := validMinified()[source]
	return min, ok
}
