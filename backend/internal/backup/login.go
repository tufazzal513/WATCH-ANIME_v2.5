package backup

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var (
	ErrLoginPending       = errors.New("telegram login is not pending")
	ErrLoginExpired       = errors.New("telegram login challenge expired")
	ErrPasswordRequired   = errors.New("telegram two-factor password required")
	ErrUnexpectedPassword = errors.New("telegram login is not awaiting a two-factor password")
)

// LoginTransport is the narrow auth surface implemented by the MTProto adapter.
// Implementations must keep authorization secrets out of logs and errors.
type LoginTransport interface {
	RequestCode(context.Context, string) (challengeID string, err error)
	SignIn(context.Context, string, string, string) (authorized bool, passwordRequired bool, err error)
	CheckPassword(context.Context, string) (authorized bool, err error)
	ExportSession(context.Context) ([]byte, error)
	Close() error
}

type LoginTransportFactory func(context.Context) (LoginTransport, error)

type loginState struct {
	transport   LoginTransport
	phone       string
	challenge   string
	expires     time.Time
	awaiting2FA bool
}

// LoginFlow coordinates the phone -> code -> optional 2FA sequence. It is
// process-local by design: a restart expires an unfinished login safely.
type LoginFlow struct {
	mu      sync.Mutex
	factory LoginTransportFactory
	store   SessionFileStore
	ttl     time.Duration
	now     func() time.Time
	pending *loginState
}

func NewLoginFlow(factory LoginTransportFactory, store SessionFileStore, ttl time.Duration) (*LoginFlow, error) {
	if factory == nil || strings.TrimSpace(store.Path) == "" {
		return nil, errors.New("telegram login requires a transport factory and session store")
	}
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	return &LoginFlow{factory: factory, store: store, ttl: ttl, now: time.Now}, nil
}

// Begin requests a login code. Only one pending login may exist at a time.
func (f *LoginFlow) Begin(ctx context.Context, phone string) error {
	phone = strings.TrimSpace(phone)
	if phone == "" || len(phone) > 32 || !strings.HasPrefix(phone, "+") {
		return errors.New("phone must be an international number beginning with +")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.expireLocked()
	if f.pending != nil {
		return errors.New("a Telegram login is already pending")
	}
	t, err := f.factory(ctx)
	if err != nil {
		return fmt.Errorf("create Telegram login client: %w", err)
	}
	challenge, err := t.RequestCode(ctx, phone)
	if err != nil {
		_ = t.Close()
		return fmt.Errorf("request Telegram login code: %w", err)
	}
	if strings.TrimSpace(challenge) == "" {
		_ = t.Close()
		return errors.New("Telegram returned an empty login challenge")
	}
	f.pending = &loginState{transport: t, phone: phone, challenge: challenge, expires: f.now().Add(f.ttl)}
	return nil
}

// SubmitCode validates the login code and persists the session only after the
// transport confirms that the account is authorized.
func (f *LoginFlow) SubmitCode(ctx context.Context, code string) error {
	code = strings.TrimSpace(code)
	if code == "" || len(code) > 32 {
		return errors.New("invalid Telegram login code")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.pendingLocked()
	if err != nil {
		return err
	}
	if p.awaiting2FA {
		return ErrPasswordRequired
	}
	authorized, passwordRequired, err := p.transport.SignIn(ctx, p.phone, p.challenge, code)
	if err != nil {
		return fmt.Errorf("Telegram code sign-in failed: %w", err)
	}
	if passwordRequired {
		p.awaiting2FA = true
		return ErrPasswordRequired
	}
	if !authorized {
		return errors.New("Telegram did not authorize this session")
	}
	return f.finishLocked(ctx, p)
}

// SubmitPassword completes Telegram's optional cloud-password (2FA) step.
func (f *LoginFlow) SubmitPassword(ctx context.Context, password string) error {
	if password == "" || len(password) > 1024 {
		return errors.New("invalid Telegram two-factor password")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.pendingLocked()
	if err != nil {
		return err
	}
	if !p.awaiting2FA {
		return ErrUnexpectedPassword
	}
	authorized, err := p.transport.CheckPassword(ctx, password)
	if err != nil {
		return fmt.Errorf("Telegram two-factor verification failed: %w", err)
	}
	if !authorized {
		return errors.New("Telegram did not authorize this session")
	}
	return f.finishLocked(ctx, p)
}

func (f *LoginFlow) Cancel() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pending == nil {
		return nil
	}
	p := f.pending
	f.pending = nil
	return p.transport.Close()
}

func (f *LoginFlow) pendingLocked() (*loginState, error) {
	if f.pending == nil {
		return nil, ErrLoginPending
	}
	if !f.now().Before(f.pending.expires) {
		p := f.pending
		f.pending = nil
		_ = p.transport.Close()
		return nil, ErrLoginExpired
	}
	return f.pending, nil
}

func (f *LoginFlow) expireLocked() {
	if f.pending != nil && !f.now().Before(f.pending.expires) {
		p := f.pending
		f.pending = nil
		_ = p.transport.Close()
	}
}

func (f *LoginFlow) finishLocked(ctx context.Context, p *loginState) error {
	session, err := p.transport.ExportSession(ctx)
	if err != nil {
		return fmt.Errorf("export authorized Telegram session: %w", err)
	}
	if err := f.store.StoreSession(session); err != nil {
		return fmt.Errorf("save authorized Telegram session: %w", err)
	}
	f.pending = nil
	if err := p.transport.Close(); err != nil {
		return fmt.Errorf("close Telegram login client: %w", err)
	}
	return nil
}
