package wsorigin

import (
	"net/http"
	"net/url"
	"strings"
)

func CheckSameHost(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	if err != nil {
		return false
	}
	if u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

func SecHeaders() http.Header {
	return http.Header{"X-Content-Type-Options": []string{"nosniff"}}
}
