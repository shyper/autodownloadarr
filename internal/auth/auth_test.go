package auth

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shyper/autodownloadarr/internal/db"
)

// cheap parameters keep the tests fast.
var testParams = Argon2Params{Memory: 64, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newService(t *testing.T) (*Service, *sql.DB, *clock) {
	t.Helper()
	sqldb, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = sqldb.Close() })
	c := &clock{t: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)}
	svc, err := NewService(sqldb, WithArgon2Params(testParams), WithClock(c.now), WithSessionTTL(24*time.Hour))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, sqldb, c
}

func TestHashAndVerify(t *testing.T) {
	hash, err := HashPassword("correct horse", testParams)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=64,t=1,p=1$") {
		t.Errorf("unexpected hash format %q", hash)
	}
	if ok, err := VerifyPassword("correct horse", hash); !ok || err != nil {
		t.Errorf("correct password rejected: %v", err)
	}
	if ok, _ := VerifyPassword("wrong horse", hash); ok {
		t.Error("wrong password accepted")
	}
	other, _ := HashPassword("correct horse", testParams)
	if other == hash {
		t.Error("salt is not random")
	}
	for _, bad := range []string{"", "plain", "$argon2i$v=19$m=64,t=1,p=1$c2FsdA$a2V5", "$argon2id$v=19$m=x$c2FsdA$a2V5"} {
		if _, err := VerifyPassword("x", bad); err == nil {
			t.Errorf("malformed hash %q accepted", bad)
		}
	}
}

func TestSetupFlow(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()

	if req, err := svc.SetupRequired(ctx); err != nil || !req {
		t.Fatalf("SetupRequired = %v, %v; want true", req, err)
	}
	if _, err := svc.CreateInitialAdmin(ctx, "bad name!", "longenough"); !errors.Is(err, ErrInvalidUsername) {
		t.Errorf("invalid username: got %v", err)
	}
	if _, err := svc.CreateInitialAdmin(ctx, "admin", "short"); !errors.Is(err, ErrWeakPassword) {
		t.Errorf("weak password: got %v", err)
	}
	u, err := svc.CreateInitialAdmin(ctx, "admin", "longenough")
	if err != nil {
		t.Fatalf("CreateInitialAdmin: %v", err)
	}
	if u.Role != RoleAdmin || u.Username != "admin" || u.ID == 0 {
		t.Errorf("unexpected user %+v", u)
	}
	if _, err := svc.CreateInitialAdmin(ctx, "second", "longenough"); !errors.Is(err, ErrSetupCompleted) {
		t.Errorf("second setup: got %v", err)
	}
	if req, _ := svc.SetupRequired(ctx); req {
		t.Error("setup still required")
	}
}

func TestAuthenticate(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()
	_, _ = svc.CreateInitialAdmin(ctx, "Admin", "longenough")

	u, err := svc.Authenticate(ctx, "admin", "longenough") // usernames are case-insensitive
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if u.LastLoginAt == nil {
		t.Error("last login not recorded")
	}
	for _, c := range []struct{ user, pass string }{
		{"admin", "wrongpassword"},
		{"nobody", "longenough"},
		{"admin", strings.Repeat("x", MaxPasswordLength+1)},
	} {
		if _, err := svc.Authenticate(ctx, c.user, c.pass); !errors.Is(err, ErrInvalidCredentials) {
			t.Errorf("Authenticate(%q) = %v, want ErrInvalidCredentials", c.user, err)
		}
	}
}

func TestSessionLifecycle(t *testing.T) {
	svc, _, clk := newService(t)
	ctx := context.Background()
	u, _ := svc.CreateInitialAdmin(ctx, "admin", "longenough")

	token, sess, err := svc.CreateSession(ctx, u.ID, "test-agent", "192.0.2.1")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	got, gotSess, err := svc.ValidateSession(ctx, token)
	if err != nil || got.ID != u.ID || gotSess.ID != sess.ID {
		t.Fatalf("ValidateSession = %+v, %+v, %v", got, gotSess, err)
	}
	if _, _, err := svc.ValidateSession(ctx, token+"x"); !errors.Is(err, ErrInvalidSession) {
		t.Errorf("tampered token: got %v", err)
	}

	// Activity extends the session (sliding expiry).
	clk.advance(20 * time.Hour)
	if _, _, err := svc.ValidateSession(ctx, token); err != nil {
		t.Fatalf("session expired too early: %v", err)
	}
	clk.advance(20 * time.Hour)
	if _, _, err := svc.ValidateSession(ctx, token); err != nil {
		t.Fatalf("activity did not extend session: %v", err)
	}
	clk.advance(25 * time.Hour)
	if _, _, err := svc.ValidateSession(ctx, token); !errors.Is(err, ErrInvalidSession) {
		t.Errorf("idle session still valid: %v", err)
	}

	token2, _, _ := svc.CreateSession(ctx, u.ID, "", "")
	if err := svc.DeleteSession(ctx, token2); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.ValidateSession(ctx, token2); !errors.Is(err, ErrInvalidSession) {
		t.Errorf("deleted session still valid: %v", err)
	}
}

func TestChangePasswordEndsOtherSessions(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()
	u, _ := svc.CreateInitialAdmin(ctx, "admin", "longenough")
	current, sess, _ := svc.CreateSession(ctx, u.ID, "", "")
	other, _, _ := svc.CreateSession(ctx, u.ID, "", "")

	if err := svc.ChangePassword(ctx, u.ID, sess.ID, "wrongpassword", "newpassword"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("wrong current password: got %v", err)
	}
	if err := svc.ChangePassword(ctx, u.ID, sess.ID, "longenough", "short"); !errors.Is(err, ErrWeakPassword) {
		t.Errorf("weak new password: got %v", err)
	}
	if err := svc.ChangePassword(ctx, u.ID, sess.ID, "longenough", "newpassword"); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	if _, _, err := svc.ValidateSession(ctx, current); err != nil {
		t.Errorf("current session ended: %v", err)
	}
	if _, _, err := svc.ValidateSession(ctx, other); !errors.Is(err, ErrInvalidSession) {
		t.Errorf("other session survived: %v", err)
	}
	if _, err := svc.Authenticate(ctx, "admin", "newpassword"); err != nil {
		t.Errorf("new password rejected: %v", err)
	}
}

func TestResetPassword(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()
	u, _ := svc.CreateInitialAdmin(ctx, "admin", "longenough")
	token, _, _ := svc.CreateSession(ctx, u.ID, "", "")

	if err := svc.ResetPassword(ctx, "ghost", "newpassword"); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("unknown user: got %v", err)
	}
	if err := svc.ResetPassword(ctx, "admin", "newpassword"); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}
	if _, _, err := svc.ValidateSession(ctx, token); !errors.Is(err, ErrInvalidSession) {
		t.Error("sessions survived reset")
	}
	if _, err := svc.Authenticate(ctx, "admin", "newpassword"); err != nil {
		t.Errorf("new password rejected: %v", err)
	}
}

func TestPurgeExpiredSessions(t *testing.T) {
	svc, sqldb, clk := newService(t)
	ctx := context.Background()
	u, _ := svc.CreateInitialAdmin(ctx, "admin", "longenough")
	_, _, _ = svc.CreateSession(ctx, u.ID, "", "")
	clk.advance(25 * time.Hour)
	if err := svc.PurgeExpiredSessions(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = sqldb.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&n)
	if n != 0 {
		t.Errorf("%d sessions left", n)
	}
}

func TestLimiter(t *testing.T) {
	clk := &clock{t: time.Unix(0, 0)}
	l := NewLimiter(3, time.Minute)
	l.now = clk.now

	for range 3 {
		if ok, _ := l.Allow("ip"); !ok {
			t.Fatal("blocked too early")
		}
		l.Fail("ip")
	}
	ok, wait := l.Allow("ip")
	if ok || wait != time.Minute {
		t.Errorf("Allow = %v, %s; want blocked for 1m", ok, wait)
	}
	if ok, _ := l.Allow("other-ip"); !ok {
		t.Error("other key blocked")
	}

	clk.advance(time.Minute)
	if ok, _ := l.Allow("ip"); !ok {
		t.Error("still blocked after window")
	}

	l.Fail("ip")
	l.Reset("ip")
	l.Fail("gone")
	clk.advance(2 * time.Minute)
	l.Prune()
	if len(l.failures) != 0 {
		t.Errorf("prune left %d keys", len(l.failures))
	}
}

func TestSetupToken(t *testing.T) {
	tok := NewSetupToken()
	if len(tok) != 14 || tok[4] != '-' || tok[9] != '-' {
		t.Fatalf("unexpected token format %q", tok)
	}
	if !SetupTokenMatches(tok, strings.ToLower(strings.ReplaceAll(tok, "-", " "))) {
		t.Error("normalised token rejected")
	}
	if !SetupTokenMatches("OIAA-BBBB-CCCC", "01aa bbbb cccc") {
		t.Error("0/1 not read as O/I")
	}
	if SetupTokenMatches(tok, "AAAA-BBBB-CCCC") {
		t.Error("wrong token accepted")
	}
	if SetupTokenMatches("", "") {
		t.Error("empty token accepted")
	}
}
