package api

import "testing"

func TestTranslateWLControl(t *testing.T) {
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

	t.Run("DISCONNECT becomes fatal upstream-disconnect error", func(t *testing.T) {
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
