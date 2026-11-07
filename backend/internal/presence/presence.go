// Package presence keeps "who is online" in memory only. Nothing is written to disk and nothing is replicated.
package presence

import (
	"sort"
	"sync"
	"time"
)

const (
	// LiveWindow is how recently a heartbeat must have arrived for a user to count as "live" (same as the old admin UI).
	LiveWindow = 90 * time.Second
	// DefaultTTL is how long an entry is kept in memory after the last heartbeat.
	DefaultTTL = 10 * time.Minute
)

type Entry struct {
	UID      string    `json:"uid"`
	Name     string    `json:"name"`
	LastSeen time.Time `json:"lastSeen"`
}

type Store struct {
	mu  sync.RWMutex
	m   map[string]Entry
	ttl time.Duration
	now func() time.Time
	ver uint64
}

func New(ttl time.Duration) *Store {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Store{m: map[string]Entry{}, ttl: ttl, now: time.Now}
}

// Touch records a heartbeat. Names are truncated so a client cannot bloat memory.
func (s *Store) Touch(uid, name string) {
	if uid == "" {
		return
	}
	if r := []rune(name); len(r) > 80 {
		name = string(r[:80])
	}
	s.mu.Lock()
	s.m[uid] = Entry{UID: uid, Name: name, LastSeen: s.now()}
	s.ver++
	s.mu.Unlock()
}

func (s *Store) Remove(uid string) {
	s.mu.Lock()
	if _, ok := s.m[uid]; ok {
		delete(s.m, uid)
		s.ver++
	}
	s.mu.Unlock()
}

// Get returns the entry for uid if it has not expired.
func (s *Store) Get(uid string) (Entry, bool) {
	s.mu.RLock()
	e, ok := s.m[uid]
	s.mu.RUnlock()
	if !ok || s.now().Sub(e.LastSeen) > s.ttl {
		return Entry{}, false
	}
	return e, true
}

// IsLive reports whether uid sent a heartbeat within LiveWindow.
func (s *Store) IsLive(uid string) bool {
	e, ok := s.Get(uid)
	return ok && s.now().Sub(e.LastSeen) <= LiveWindow
}

// Live returns users whose last heartbeat is within window (use LiveWindow), newest first.
func (s *Store) Live(window time.Duration) []Entry {
	now := s.now()
	s.mu.RLock()
	out := make([]Entry, 0, len(s.m))
	for _, e := range s.m {
		if d := now.Sub(e.LastSeen); d <= window && d <= s.ttl {
			out = append(out, e)
		}
	}
	s.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].LastSeen.Equal(out[j].LastSeen) {
			return out[i].UID < out[j].UID
		}
		return out[i].LastSeen.After(out[j].LastSeen)
	})
	return out
}

func (s *Store) CountLive() int { return len(s.Live(LiveWindow)) }

// Prune drops expired entries and returns how many were removed.
func (s *Store) Prune() int {
	now := s.now()
	n := 0
	s.mu.Lock()
	for k, e := range s.m {
		if now.Sub(e.LastSeen) > s.ttl {
			delete(s.m, k)
			n++
		}
	}
	if n > 0 {
		s.ver++
	}
	s.mu.Unlock()
	return n
}

// Version changes whenever the set changes (usable for cheap change detection / ETags).
func (s *Store) Version() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ver
}
