package webassets

import "net/http"

// Temporary aliases for callers outside this package that still use the old
// names. Delete this file once those call sites use the new names.

func AceitaBrotli(r *http.Request) bool { return AcceptsBrotli(r) }

func AquecePreCompressao() { WarmPrecompression() }

func MinificadoDe(source string) (string, bool) { return MinifiedOf(source) }
