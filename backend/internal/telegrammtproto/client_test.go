package telegrammtproto

import (
	"errors"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"movex/internal/backup"
)

func TestNewRequiresCompleteMTProtoConfiguration(t *testing.T) {
	cases := []Config{
		{},
		{APIID: 123, APIHash: "hash"},
		{APIID: 123, ChatID: "@movex_backups"},
		{APIID: 123, APIHash: "hash", ChatID: "@movex_backups"},
	}
	for i, cfg := range cases {
		if _, err := New(cfg); err == nil {
			t.Fatalf("case %d: expected configuration error", i)
		}
	}
	if _, err := New(Config{APIID: 123, APIHash: "hash", ChatID: "@movex_backups", SessionPath: "/tmp/telegram.session"}); err != nil {
		t.Fatalf("valid configuration rejected: %v", err)
	}
}

func TestSentMessageIDReadsTelegramUpdateShapes(t *testing.T) {
	cases := []struct {
		name string
		resp tg.UpdatesClass
		want int64
	}{
		{
			name: "regular message",
			resp: &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateNewMessage{Message: &tg.Message{ID: 41}}}},
			want: 41,
		},
		{
			name: "channel message",
			resp: &tg.UpdatesCombined{Updates: []tg.UpdateClass{&tg.UpdateNewChannelMessage{Message: &tg.Message{ID: 42}}}},
			want: 42,
		},
		{
			name: "short sent message",
			resp: &tg.UpdateShortSentMessage{ID: 43},
			want: 43,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := sentMessageID(tc.resp)
			if err != nil || got != tc.want {
				t.Fatalf("sentMessageID() = %d, %v; want %d", got, err, tc.want)
			}
		})
	}
	if _, err := sentMessageID(&tg.Updates{}); err == nil {
		t.Fatal("empty updates must not silently return message ID zero")
	}
}

func TestMapFloodWaitPreservesRetryDuration(t *testing.T) {
	got := mapFloodWait(tgerr.New(420, "FLOOD_WAIT_17"))
	var wait *backup.FloodWaitError
	if !errors.As(got, &wait) {
		t.Fatalf("expected FloodWaitError, got %T (%v)", got, got)
	}
	if wait.Wait != 17*time.Second {
		t.Fatalf("wait = %s, want 17s", wait.Wait)
	}
}
