package api

// stt_test.go — armour for WhisperLive's control-signal contract.
//
// The ack of the caption round-trip (videocall.js) anchors on the SERVER_READY
// signal translated into {type:"ready"}. If a heal (or a refactor) removes that
// mapping, the transcription-activation confirmation stops working IN SILENCE —
// exactly the class of bug this fixes. This test fails loudly before that can
// reach a deploy.

import "testing"

func TestTranslateWLControl(t *testing.T) {
	// SERVER_READY → {type:"ready"} is the ack's anchor. Do NOT remove it without
	// breaking the cross-user caption confirmation.
	t.Run("SERVER_READY becomes ready with backend", func(t *testing.T) {
		payload, handled := translateWLControl(wlUpdateMsg{Message: "SERVER_READY", Backend: "medium"})
		if !handled {
			t.Fatal("SERVER_READY should be treated as a control signal")
		}
		if payload["type"] != "ready" {
			t.Fatalf("type expected \"ready\", got %v", payload["type"])
		}
		if payload["backend"] != "medium" {
			t.Fatalf("backend expected \"medium\", got %v", payload["backend"])
		}
	})

	t.Run("DISCONNECT vira error fatal upstream-disconnect", func(t *testing.T) {
		payload, handled := translateWLControl(wlUpdateMsg{Message: "DISCONNECT"})
		if !handled {
			t.Fatal("DISCONNECT should be treated as a control signal")
		}
		if payload["type"] != "error" {
			t.Fatalf("type expected \"error\", got %v", payload["type"])
		}
		if payload["code"] != "upstream-disconnect" {
			t.Fatalf("code expected \"upstream-disconnect\", got %v", payload["code"])
		}
		if payload["fatal"] != true {
			t.Fatalf("fatal expected true, got %v", payload["fatal"])
		}
	})

	// WAIT / "" / data messages are NOT control — they follow the normal segment
	// translation flow. handled=false guarantees we do not swallow them.
	for _, msg := range []string{"WAIT", "", "SERVER_OVERLOADED"} {
		t.Run("non-control passes straight through:"+msg, func(t *testing.T) {
			payload, handled := translateWLControl(wlUpdateMsg{Message: msg})
			if handled {
				t.Fatalf("message %q should not be treated as control", msg)
			}
			if payload != nil {
				t.Fatalf("payload expected nil for %q, got %v", msg, payload)
			}
		})
	}
}
