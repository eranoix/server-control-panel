package webassets

import (
	"embed"
	"io/fs"
	"strconv"
	"time"
)

//go:embed web/*
var FS embed.FS

//go:embed docs/report.html
var docsFS embed.FS

func DocsReport() ([]byte, error) { return docsFS.ReadFile("docs/report.html") }

var BuildStamp = strconv.FormatInt(time.Now().Unix(), 10)

var ProcessStartTime = time.Now()

func SubFS() fs.FS {
	sub, _ := fs.Sub(FS, "web")
	return sub
}

func ReadFile(path string) ([]byte, error) {
	return FS.ReadFile(path)
}
