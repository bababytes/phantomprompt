package mask

import (
	"testing"
	"time"
)

// A protected terminal touches its session every 30 s; it must survive a long working day.
func TestSessionLifetimeIsPerSession(t *testing.T) {
	now := time.Now()
	store := NewStore(30*time.Minute, 4*time.Hour, 8, DefaultPolicy())
	store.now = func() time.Time { return now }
	terminal, err := store.CreateFor(DefaultPolicy(), 4096, 1<<20, 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := store.Create(DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for elapsed := time.Duration(0); elapsed < 5*time.Hour; elapsed += 30 * time.Second {
		now = now.Add(30 * time.Second)
		for _, id := range []string{terminal, gateway} {
			if _, release, err := store.Acquire(id, "cli"); err == nil {
				release()
			}
		}
	}
	if _, release, err := store.Acquire(terminal, "cli"); err != nil {
		t.Fatalf("active terminal lost its session after 5 h: %v", err)
	} else {
		release()
	}
	if _, _, err := store.Acquire(gateway, "cli"); err != ErrSession {
		t.Fatal("default lifetime no longer applies to other sessions")
	}
}
