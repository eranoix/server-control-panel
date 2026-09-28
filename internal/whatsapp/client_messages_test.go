package whatsapp

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSendFileJSONContract(t *testing.T) {
	cases := []struct {
		name        string
		msgType     string
		caption     string
		quotedID    string
		wantPath    string
		wantConvert bool
		wantCaption bool
		wantReply   bool
	}{
		{"image with caption+reply", "image", "hello", "ABC123", "/api/sendImage", false, true, true},
		{"document plain", "document", "", "", "/api/sendFile", false, false, false},
		{"voice converts", "voice", "", "", "/api/sendVoice", true, false, false},
		{"video converts", "video", "caption", "", "/api/sendVideo", true, true, false},
		{"unknown falls back to image", "weird", "", "", "/api/sendImage", false, false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := []byte("the-raw-bytes-\x00\x01\x02")
			var gotPath, gotCT string
			var gotBody map[string]any

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				gotCT = r.Header.Get("Content-Type")
				raw, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(raw, &gotBody)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"true_5511999998888@c.us_WAMID01"}`))
			}))
			defer srv.Close()

			c := NewClient(srv.URL, "secret-key")
			id, err := c.SendFile("5511999998888@c.us", tc.msgType, "photo.jpg", "image/jpeg", tc.caption, tc.quotedID, payload)
			if err != nil {
				t.Fatalf("SendFile: %v", err)
			}
			if id != "true_5511999998888@c.us_WAMID01" {
				t.Errorf("id = %q, want parsed flexible id", id)
			}

			if gotPath != tc.wantPath {
				t.Errorf("path = %q, want %q", gotPath, tc.wantPath)
			}
			if gotCT != "application/json" {
				t.Errorf("Content-Type = %q, want application/json (multipart regression?)", gotCT)
			}
			if got := gotBody["chatId"]; got != "5511999998888@s.whatsapp.net" {
				t.Errorf("chatId = %v, want normalized @s.whatsapp.net", got)
			}
			if got := gotBody["session"]; got != "default" {
				t.Errorf("session = %v, want default", got)
			}
			file, ok := gotBody["file"].(map[string]any)
			if !ok {
				t.Fatalf("file missing/!object: %v", gotBody["file"])
			}
			if file["mimetype"] != "image/jpeg" || file["filename"] != "photo.jpg" {
				t.Errorf("file meta = %v, want mimetype/filename preserved", file)
			}
			dataStr, _ := file["data"].(string)
			dec, err := base64.StdEncoding.DecodeString(dataStr)
			if err != nil {
				t.Errorf("file.data is not raw std base64: %v", err)
			} else if string(dec) != string(payload) {
				t.Errorf("decoded file.data = %q, want %q", dec, payload)
			}
			_, hasConvert := gotBody["convert"]
			if hasConvert != tc.wantConvert {
				t.Errorf("convert present = %v, want %v", hasConvert, tc.wantConvert)
			}
			if tc.wantConvert && gotBody["convert"] != true {
				t.Errorf("convert = %v, want true", gotBody["convert"])
			}
			_, hasCaption := gotBody["caption"]
			if hasCaption != tc.wantCaption {
				t.Errorf("caption present = %v, want %v", hasCaption, tc.wantCaption)
			}
			_, hasReply := gotBody["reply_to"]
			if hasReply != tc.wantReply {
				t.Errorf("reply_to present = %v, want %v", hasReply, tc.wantReply)
			}
		})
	}
}
