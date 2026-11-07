package presence

import (
	"testing"
	"time"
)

func newClock() (*Store, *time.Time) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s := New(10 * time.Minute)
	s.now = func() time.Time { return now }
	return s, &now
}

func TestLiveWindowAndTTL(t *testing.T) {
	s, now := newClock()
	s.Touch("a", "Alice")
	if !s.IsLive("a") || s.CountLive() != 1 {
		t.Fatal("fresh heartbeat must be live")
	}
	*now = now.Add(91 * time.Second)
	if s.IsLive("a") || s.CountLive() != 0 {
		t.Fatal("older than 90s must not be live")
	}
	if _, ok := s.Get("a"); !ok {
		t.Fatal("entry stays readable until TTL")
	}
	*now = now.Add(10 * time.Minute)
	if _, ok := s.Get("a"); ok {
		t.Fatal("expired after TTL")
	}
	if n := s.Prune(); n != 1 {
		t.Fatalf("prune removed %d", n)
	}
}

func TestOrderingRemoveAndVersion(t *testing.T) {
	s, now := newClock()
	s.Touch("a", "A")
	*now = now.Add(10 * time.Second)
	s.Touch("b", "B")
	l := s.Live(LiveWindow)
	if len(l) != 2 || l[0].UID != "b" {
		t.Fatalf("newest first expected: %+v", l)
	}
	v := s.Version()
	s.Remove("a")
	if s.Version() == v || s.CountLive() != 1 {
		t.Fatal("remove must change version and set")
	}
	v = s.Version()
	s.Remove("ghost")
	if s.Version() != v {
		t.Fatal("removing unknown uid must not bump version")
	}
}

func TestIgnoresEmptyUIDAndTruncatesName(t *testing.T) {
	s, _ := newClock()
	s.Touch("", "x")
	if s.CountLive() != 0 {
		t.Fatal("empty uid ignored")
	}
	long := make([]rune, 500)
	for i := range long {
		long[i] = 'অ'
	}
	s.Touch("u", string(long))
	e, _ := s.Get("u")
	if len([]rune(e.Name)) != 80 {
		t.Fatalf("name len %d", len([]rune(e.Name)))
	}
}
