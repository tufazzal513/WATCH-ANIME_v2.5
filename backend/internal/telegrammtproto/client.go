// Package telegrammtproto provides the production Telegram MTProto transport
// used by MoveX backups. It uses a user session (not a Bot API token).
package telegrammtproto

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/telegram"
	gotdauth "github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/telegram/message/html"
	"github.com/gotd/td/telegram/message/peer"
	"github.com/gotd/td/telegram/query"
	querymessages "github.com/gotd/td/telegram/query/messages"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"movex/internal/backup"
)

// Config contains only the settings needed by a single Telegram user account.
// SessionPath must be on persistent storage and protected with mode 0600.
type Config struct {
	APIID       int
	APIHash     string
	ChatID      string
	SessionPath string
}

type Client struct{ cfg Config }

func New(cfg Config) (*Client, error) {
	cfg.ChatID = strings.TrimSpace(cfg.ChatID)
	if cfg.APIID <= 0 || strings.TrimSpace(cfg.APIHash) == "" {
		return nil, errors.New("Telegram API ID and API hash are required")
	}
	if cfg.ChatID == "" {
		return nil, errors.New("Telegram backup chat ID is required")
	}
	if strings.TrimSpace(cfg.SessionPath) == "" {
		return nil, errors.New("Telegram session path is required")
	}
	return &Client{cfg: cfg}, nil
}

func (c *Client) newClient() *telegram.Client {
	return telegram.NewClient(c.cfg.APIID, c.cfg.APIHash, telegram.Options{
		SessionStorage: backup.SessionFileStore{Path: c.cfg.SessionPath},
		NoUpdates:      true,
	})
}

func (c *Client) withClient(ctx context.Context, fn func(context.Context, *telegram.Client) error) error {
	client := c.newClient()
	err := client.Run(ctx, func(runCtx context.Context) error {
		status, err := client.Auth().Status(runCtx)
		if err != nil {
			return fmt.Errorf("check Telegram authorization: %w", err)
		}
		if !status.Authorized {
			return errors.New("Telegram account is not authorized; complete login from the admin panel")
		}
		return fn(runCtx, client)
	})
	if err != nil {
		return mapFloodWait(err)
	}
	return nil
}

// RequestCode starts phone-code authorization and returns Telegram's opaque
// phone_code_hash. The hash is held only in LoginFlow memory until login ends.
func (c *Client) RequestCode(ctx context.Context, phone string) (string, error) {
	var challenge string
	client := c.newClient()
	err := client.Run(ctx, func(runCtx context.Context) error {
		sent, err := client.Auth().SendCode(runCtx, phone, gotdauth.SendCodeOptions{})
		if err != nil {
			return err
		}
		code, ok := sent.(*tg.AuthSentCode)
		if !ok {
			return fmt.Errorf("unsupported Telegram login challenge %T", sent)
		}
		challenge = code.PhoneCodeHash
		if challenge == "" {
			return errors.New("Telegram returned an empty phone code hash")
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return challenge, nil
}

func (c *Client) SignIn(ctx context.Context, phone, challenge, code string) (authorized, passwordRequired bool, err error) {
	client := c.newClient()
	err = client.Run(ctx, func(runCtx context.Context) error {
		_, signErr := client.Auth().SignIn(runCtx, phone, code, challenge)
		if errors.Is(signErr, gotdauth.ErrPasswordAuthNeeded) {
			passwordRequired = true
			return nil
		}
		if signErr != nil {
			return signErr
		}
		authorized = true
		return nil
	})
	return authorized, passwordRequired, err
}

func (c *Client) CheckPassword(ctx context.Context, password string) (authorized bool, err error) {
	client := c.newClient()
	err = client.Run(ctx, func(runCtx context.Context) error {
		if _, err := client.Auth().Password(runCtx, password); err != nil {
			return err
		}
		authorized = true
		return nil
	})
	return authorized, err
}

func (c *Client) ExportSession(context.Context) ([]byte, error) {
	b, err := os.ReadFile(c.cfg.SessionPath)
	if err != nil {
		return nil, fmt.Errorf("read Telegram session: %w", err)
	}
	if len(b) == 0 {
		return nil, errors.New("Telegram session storage is empty")
	}
	return b, nil
}

// Close is intentionally a no-op: each operation owns and closes its gotd
// client inside Client.Run. It satisfies backup.LoginTransport.
func (c *Client) Close() error { return nil }

func mapFloodWait(err error) error {
	if rpcErr, ok := tgerr.As(err); ok && (rpcErr.Type == "FLOOD_WAIT" || rpcErr.Type == "FLOOD_PREMIUM_WAIT") && rpcErr.Argument > 0 {
		return &backup.FloodWaitError{Wait: time.Duration(rpcErr.Argument) * time.Second, Err: err}
	}
	return err
}

var _ backup.LoginTransport = (*Client)(nil)
var _ backup.TelegramClient = (*Client)(nil)

func (c *Client) UploadDocument(ctx context.Context, filename, caption string, src io.Reader, size int64) (int64, error) {
	if filename == "" || src == nil || size <= 0 {
		return 0, errors.New("Telegram upload requires filename, reader and positive size")
	}
	var messageID int64
	err := c.withClient(ctx, func(runCtx context.Context, client *telegram.Client) error {
		api := client.API()
		up := uploader.NewUploader(api)
		file, err := up.Upload(runCtx, uploader.NewUpload(filename, io.LimitReader(src, size), size))
		if err != nil {
			return fmt.Errorf("upload backup document: %w", err)
		}
		doc := message.UploadedDocument(file, html.String(nil, caption)).Filename(filename)
		updates, err := message.NewSender(api).WithUploader(up).Resolve(c.cfg.ChatID).Media(runCtx, doc)
		if err != nil {
			return fmt.Errorf("send backup document: %w", err)
		}
		messageID, err = sentMessageID(updates)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return messageID, nil
}

func sentMessageID(updates tg.UpdatesClass) (int64, error) {
	var found int64
	consume := func(update tg.UpdateClass) {
		switch value := update.(type) {
		case *tg.UpdateNewMessage:
			if value.Message != nil {
				found = int64(value.Message.GetID())
			}
		case *tg.UpdateNewChannelMessage:
			if value.Message != nil {
				found = int64(value.Message.GetID())
			}
		}
	}
	switch value := updates.(type) {
	case *tg.Updates:
		for _, update := range value.Updates {
			consume(update)
		}
	case *tg.UpdatesCombined:
		for _, update := range value.Updates {
			consume(update)
		}
	case *tg.UpdateShortSentMessage:
		found = int64(value.ID)
	}
	if found <= 0 {
		return 0, fmt.Errorf("Telegram upload succeeded but response did not contain a message ID (%T)", updates)
	}
	return found, nil
}

func (c *Client) resolvePeer(ctx context.Context, api *tg.Client) (tg.InputPeerClass, error) {
	// Numeric Telegram dialog IDs (for example -1001234567890) do not carry
	// access_hash values. Resolve them from the authenticated account's joined
	// dialogs rather than guessing or constructing an incomplete channel peer.
	if strings.HasPrefix(c.cfg.ChatID, "-100") || (strings.HasPrefix(c.cfg.ChatID, "-") && len(c.cfg.ChatID) >= 6) {
		marked, err := strconv.ParseInt(c.cfg.ChatID, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid numeric Telegram chat ID: %w", err)
		}
		dialogs, err := api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
			OffsetPeer: &tg.InputPeerEmpty{}, Limit: 500, Hash: 0,
		})
		if err != nil {
			return nil, fmt.Errorf("list Telegram dialogs to resolve numeric chat ID: %w", err)
		}
		var chats []tg.ChatClass
		switch value := dialogs.(type) {
		case *tg.MessagesDialogs:
			chats = value.Chats
		case *tg.MessagesDialogsSlice:
			chats = value.Chats
		default:
			return nil, fmt.Errorf("unexpected Telegram dialogs response %T", dialogs)
		}
		if strings.HasPrefix(c.cfg.ChatID, "-100") {
			want := -marked - 1000000000000
			for _, chat := range chats {
				if channel, ok := chat.(*tg.Channel); ok && channel.ID == want {
					return &tg.InputPeerChannel{ChannelID: channel.ID, AccessHash: channel.AccessHash}, nil
				}
			}
		} else {
			want := -marked
			for _, chat := range chats {
				if group, ok := chat.(*tg.Chat); ok && int64(group.ID) == want {
					return &tg.InputPeerChat{ChatID: int64(group.ID)}, nil
				}
			}
		}
		return nil, fmt.Errorf("Telegram chat %s was not found in the account's joined dialogs; join the channel/group first", c.cfg.ChatID)
	}
	resolver := peer.DefaultResolver(api)
	resolved, err := peer.Resolve(resolver, c.cfg.ChatID)(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve Telegram backup chat: %w", err)
	}
	return resolved, nil
}

func (c *Client) PinMessage(ctx context.Context, messageID int64) error {
	return c.updatePin(ctx, messageID, false)
}
func (c *Client) UnpinMessage(ctx context.Context, messageID int64) error {
	return c.updatePin(ctx, messageID, true)
}
func (c *Client) updatePin(ctx context.Context, messageID int64, unpin bool) error {
	if messageID <= 0 {
		return errors.New("invalid Telegram message ID")
	}
	return c.withClient(ctx, func(runCtx context.Context, client *telegram.Client) error {
		p, err := c.resolvePeer(runCtx, client.API())
		if err != nil {
			return err
		}
		_, err = client.API().MessagesUpdatePinnedMessage(runCtx, &tg.MessagesUpdatePinnedMessageRequest{
			Peer: p, ID: int(messageID), Silent: true, Unpin: unpin,
		})
		if err != nil {
			return fmt.Errorf("update Telegram pinned message: %w", err)
		}
		return nil
	})
}

const maxRestoreHistoryMessages = 2000

var errRestoreHistoryLimit = errors.New("Telegram backup history scan limit reached")

// ListRestoreDocuments discovers only MOVEX database archives in the configured
// backup chat. Captions are treated as untrusted input and validated before a
// candidate is returned to the restore layer.
func (c *Client) ListRestoreDocuments(ctx context.Context) ([]backup.RemoteBackup, error) {
	var found []backup.RemoteBackup
	err := c.withClient(ctx, func(runCtx context.Context, client *telegram.Client) error {
		api := client.API()
		p, err := c.resolvePeer(runCtx, api)
		if err != nil {
			return err
		}
		seen := 0
		err = query.Messages(api).GetHistory(p).BatchSize(100).ForEach(runCtx, func(_ context.Context, elem querymessages.Elem) error {
			seen++
			msg, ok := elem.Msg.(*tg.Message)
			if ok {
				file, hasFile := elem.File()
				if hasFile && strings.HasPrefix(strings.ToLower(file.Name), "movex-") && strings.HasSuffix(strings.ToLower(file.Name), ".db.gz") {
					metadata, parseErr := backup.ParseTelegramBackupMetadata(msg.Message)
					if parseErr != nil {
						return fmt.Errorf("invalid MOVEX backup caption on Telegram message %d: %w", msg.ID, parseErr)
					}
					found = append(found, backup.RemoteBackup{MessageID: int64(msg.ID), Filename: filepath.Base(file.Name), Metadata: metadata})
				}
			}
			if seen >= maxRestoreHistoryMessages {
				return errRestoreHistoryLimit
			}
			return nil
		})
		if errors.Is(err, errRestoreHistoryLimit) {
			if len(found) == 0 {
				return errors.New("no MOVEX backup found in the newest 2000 Telegram messages; refusing an incomplete history scan")
			}
			return nil
		}
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("list Telegram backup documents: %w", err)
	}
	return found, nil
}

// DownloadRestoreDocument streams one matching backup document into dst. It
// resolves the message from the configured chat instead of accepting a peer or
// file location supplied by an external caller.
func (c *Client) DownloadRestoreDocument(ctx context.Context, messageID int64, dst io.Writer) error {
	if messageID <= 0 || dst == nil {
		return errors.New("Telegram restore download requires a positive message ID and destination")
	}
	return c.withClient(ctx, func(runCtx context.Context, client *telegram.Client) error {
		api := client.API()
		p, err := c.resolvePeer(runCtx, api)
		if err != nil {
			return err
		}
		var found bool
		seen := 0
		err = query.Messages(api).GetHistory(p).BatchSize(100).ForEach(runCtx, func(_ context.Context, elem querymessages.Elem) error {
			seen++
			msg, ok := elem.Msg.(*tg.Message)
			if !ok || int64(msg.ID) != messageID {
				if seen >= maxRestoreHistoryMessages {
					return errRestoreHistoryLimit
				}
				return nil
			}
			file, ok := elem.File()
			if !ok || !strings.HasPrefix(strings.ToLower(file.Name), "movex-") || !strings.HasSuffix(strings.ToLower(file.Name), ".db.gz") {
				return errors.New("Telegram message is not a MOVEX database archive")
			}
			if _, err := downloader.NewDownloader().Download(api, file.Location).Stream(runCtx, dst); err != nil {
				return fmt.Errorf("stream Telegram backup document: %w", err)
			}
			found = true
			return errRestoreHistoryLimit // stop iteration after the matching message
		})
		if errors.Is(err, errRestoreHistoryLimit) && found {
			return nil
		}
		if errors.Is(err, errRestoreHistoryLimit) && !found {
			return fmt.Errorf("Telegram backup message %d was not found in the newest %d messages", messageID, maxRestoreHistoryMessages)
		}
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("Telegram backup message %d was not found in the configured chat", messageID)
		}
		return nil
	})
}

func (c *Client) DeleteMessage(ctx context.Context, messageID int64) error {
	if messageID <= 0 {
		return errors.New("invalid Telegram message ID")
	}
	return c.withClient(ctx, func(runCtx context.Context, client *telegram.Client) error {
		api := client.API()
		p, err := c.resolvePeer(runCtx, api)
		if err != nil {
			return err
		}
		switch channel := p.(type) {
		case *tg.InputPeerChannel:
			_, err = api.ChannelsDeleteMessages(runCtx, &tg.ChannelsDeleteMessagesRequest{
				Channel: &tg.InputChannel{ChannelID: channel.ChannelID, AccessHash: channel.AccessHash},
				ID:      []int{int(messageID)},
			})
		default:
			_, err = api.MessagesDeleteMessages(runCtx, &tg.MessagesDeleteMessagesRequest{ID: []int{int(messageID)}, Revoke: true})
		}
		if err != nil {
			return fmt.Errorf("delete Telegram backup message: %w", err)
		}
		return nil
	})
}
