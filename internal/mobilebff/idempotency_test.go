package mobilebff

import (
	"testing"
	"time"
)

func TestIdempotencyRemembersAndReturnsSameResult(t *testing.T) {
	i := NewIdempotency(t.TempDir())

	if _, _, ok := i.Recall("sam:k1"); ok {
		t.Fatal("a key never seen must not be remembered")
	}

	i.Remember("sam:k1", `{"status":"ok"}`, 200)

	body, status, ok := i.Recall("sam:k1")
	if !ok {
		t.Fatal("the key should be remembered")
	}
	if body != `{"status":"ok"}` || status != 200 {
		t.Fatalf("result differs from what was stored: %q %d", body, status)
	}
}

func TestIdempotencyDoesNotLeakAcrossUsers(t *testing.T) {
	i := NewIdempotency(t.TempDir())
	i.Remember("sam:k1", `{"status":"ok"}`, 200)

	if _, _, ok := i.Recall("jordan:k1"); ok {
		t.Fatal("one user's key must not answer for another's")
	}
}

func TestIdempotencyExpiresAfterTTL(t *testing.T) {
	clock := time.Now()
	i := NewIdempotency(t.TempDir())
	i.now = func() time.Time { return clock }

	i.Remember("sam:k1", `{"status":"ok"}`, 200)
	if _, _, ok := i.Recall("sam:k1"); !ok {
		t.Fatal("right after storing, it must be valid")
	}

	clock = clock.Add(25 * time.Hour)
	if _, _, ok := i.Recall("sam:k1"); ok {
		t.Fatal("once the deadline has passed, the key is no longer valid")
	}
}

func TestIdempotencySurvivesProcessRestart(t *testing.T) {
	dir := t.TempDir()
	first := NewIdempotency(dir)
	first.Remember("sam:k1", `{"id":"abc"}`, 200)

	second := NewIdempotency(dir)
	body, status, ok := second.Recall("sam:k1")
	if !ok {
		t.Fatal("the key must survive a restart")
	}
	if body != `{"id":"abc"}` || status != 200 {
		t.Fatalf("result did not survive intact: %q %d", body, status)
	}
}

func TestIdempotencyWithCorruptFileStartsEmptyInsteadOfBreaking(t *testing.T) {
	dir := t.TempDir()
	i := NewIdempotency(dir)
	i.Remember("sam:k1", "{}", 200)

	if err := persistFile(i.file, "this is not json"); err != nil {
		t.Fatal(err)
	}
	other := NewIdempotency(dir)
	if _, _, ok := other.Recall("sam:k1"); ok {
		t.Fatal("a corrupted table must start empty")
	}
	other.Remember("sam:k2", "{}", 200)
	if _, _, ok := other.Recall("sam:k2"); !ok {
		t.Fatal("after corruption, storing must still work")
	}
}

func TestIdempotencyNilIsNoOp(t *testing.T) {
	var i *Idempotency
	i.Remember("sam:k1", "{}", 200)
	if _, _, ok := i.Recall("sam:k1"); ok {
		t.Fatal("nil remembers nothing")
	}
}
