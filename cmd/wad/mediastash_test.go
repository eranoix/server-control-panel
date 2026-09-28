package main

import (
	"path/filepath"
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

func sampleImageMsg() *waE2E.Message {
	return &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
		URL:           proto.String("https://mmg.whatsapp.net/o1/v/t24/example"),
		DirectPath:    proto.String("/o1/v/t24/f2/m231/AQPexampleDirectPath"),
		MediaKey:      []byte("0123456789abcdef0123456789abcdef"),
		Mimetype:      proto.String("image/jpeg"),
		Caption:       proto.String("Party hat 99.99"),
		FileEncSHA256: []byte("encsha256_32_bytes_padding_here!"),
		FileSHA256:    []byte("plainsha256_32_bytes_padding_okay"),
		FileLength:    proto.Uint64(252122),
	}}
}

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

func TestGetStashedFallsBackToDisk(t *testing.T) {
	dir := t.TempDir()
	s := &session{dbPath: filepath.Join(dir, "session.db"), msgs: map[string]*stashedMsg{}}

	const id = "ABCDEF0123456789"
	s.persistStashedMedia(id, "self@s.whatsapp.net", true, sampleImageMsg())

	if sm := s.getStashed(id); sm == nil {
		t.Fatal("getStashed did not fall back to disk — media would 404 after restart")
	} else if !sm.fromMe {
		t.Error("fromMe not preserved through disk fallback")
	}

	if sm := s.getStashed("does-not-exist"); sm != nil {
		t.Error("getStashed returned non-nil for unknown id")
	}
}

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
	viewOnce := &waE2E.Message{ViewOnceMessage: &waE2E.FutureProofMessage{Message: sampleImageMsg()}}
	if !hasDownloadableMedia(viewOnce) {
		t.Error("view-once image not classified as media")
	}
}
