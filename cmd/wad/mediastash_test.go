package main

import (
	"path/filepath"
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

// sampleImageMsg builds a waE2E.Message carrying an ImageMessage with the exact
// fields whatsmeow needs to re-download + decrypt from the CDN (directPath +
// mediaKey + enc hashes). This is what must survive a daemon restart.
func sampleImageMsg() *waE2E.Message {
	return &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
		URL:           proto.String("https://mmg.whatsapp.net/o1/v/t24/example"),
		DirectPath:    proto.String("/o1/v/t24/f2/m231/AQPexampleDirectPath"),
		MediaKey:      []byte("0123456789abcdef0123456789abcdef"),
		Mimetype:      proto.String("image/jpeg"),
		Caption:       proto.String("Chapéu da festa 99,99"),
		FileEncSHA256: []byte("encsha256_32_bytes_padding_here!"),
		FileSHA256:    []byte("plainsha256_32_bytes_padding_okay"),
		FileLength:    proto.Uint64(252122),
	}}
}

// TestMediaStashRoundTrip is the core proof for the bug fix: a media message's
// download keys, once persisted, reload identically from disk — so a manual
// "Baixar" works after the in-memory stash is wiped by a daemon restart.
func TestMediaStashRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := &session{dbPath: filepath.Join(dir, "session.db")}

	const id = "3A10EAFABF92EBAB4D73"
	orig := sampleImageMsg()
	s.persistStashedMedia(id, "100000000000002@lid", false, orig)

	got := s.loadPersistedStash(id)
	if got == nil {
		t.Fatal("loadPersistedStash returned nil after persist — keys did not survive")
	}
	if got.sender != "100000000000002@lid" || got.fromMe != false {
		t.Fatalf("sender/fromMe mismatch: got %q/%v", got.sender, got.fromMe)
	}
	gotImg := got.msg.GetImageMessage()
	if gotImg == nil {
		t.Fatal("reloaded message has no ImageMessage")
	}
	if gotImg.GetDirectPath() != orig.GetImageMessage().GetDirectPath() {
		t.Errorf("directPath lost: %q", gotImg.GetDirectPath())
	}
	if string(gotImg.GetMediaKey()) != string(orig.GetImageMessage().GetMediaKey()) {
		t.Error("mediaKey lost — cannot decrypt CDN media")
	}
	if string(gotImg.GetFileEncSHA256()) != string(orig.GetImageMessage().GetFileEncSHA256()) {
		t.Error("fileEncSHA256 lost")
	}
	if !hasDownloadableMedia(got.msg) {
		t.Error("hasDownloadableMedia=false for an image message")
	}
}

// TestGetStashedFallsBackToDisk proves getStashed recovers a media message from
// disk on an in-memory miss (the post-restart scenario), and still returns nil
// for an unknown id.
func TestGetStashedFallsBackToDisk(t *testing.T) {
	dir := t.TempDir()
	s := &session{dbPath: filepath.Join(dir, "session.db"), msgs: map[string]*stashedMsg{}}

	const id = "ABCDEF0123456789"
	s.persistStashedMedia(id, "self@s.whatsapp.net", true, sampleImageMsg())

	// In-memory stash is empty (simulates a fresh daemon after restart).
	if sm := s.getStashed(id); sm == nil {
		t.Fatal("getStashed did not fall back to disk — media would 404 after restart")
	} else if !sm.fromMe {
		t.Error("fromMe not preserved through disk fallback")
	}

	if sm := s.getStashed("does-not-exist"); sm != nil {
		t.Error("getStashed returned non-nil for unknown id")
	}
}

// TestHasDownloadableMedia guards the predicate that gates persistence: text
// messages must not be persisted (bounds disk growth), media must.
func TestHasDownloadableMedia(t *testing.T) {
	if hasDownloadableMedia(&waE2E.Message{Conversation: proto.String("oi")}) {
		t.Error("text message wrongly classified as downloadable media")
	}
	if hasDownloadableMedia(nil) {
		t.Error("nil message wrongly classified as media")
	}
	if !hasDownloadableMedia(sampleImageMsg()) {
		t.Error("image message not classified as media")
	}
	// View-once wrapper must be unwrapped before the type switch.
	viewOnce := &waE2E.Message{ViewOnceMessage: &waE2E.FutureProofMessage{Message: sampleImageMsg()}}
	if !hasDownloadableMedia(viewOnce) {
		t.Error("view-once image not classified as media")
	}
}
