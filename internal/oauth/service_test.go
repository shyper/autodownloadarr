package oauth

import (
	"crypto/sha256"
	"encoding/base64"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shyper/autodownloadarr/internal/db"
)

func setup(t *testing.T) (*Service, Request, string) {
	t.Helper()
	d, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "oauth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err = d.ExecContext(t.Context(), `INSERT INTO users(id,username,password_hash,role,created_at,updated_at) VALUES(1,'test','unused','admin',0,0)`); err != nil {
		t.Fatal(err)
	}
	svc := New(d, "https://media.example")
	client, err := svc.SaveClient(t.Context(), Client{Name: "Test agent", Redirects: []string{"http://127.0.0.1:4567/callback"}})
	if err != nil {
		t.Fatal(err)
	}
	verifier := strings.Repeat("a", 43)
	h := sha256.Sum256([]byte(verifier))
	return svc, Request{ClientID: client.ID, Redirect: client.Redirects[0], Scope: "media:read media:request offline_access", Challenge: base64.RawURLEncoding.EncodeToString(h[:]), Resource: svc.Resource}, verifier
}
func TestCodePKCEBindingAndRefreshReplay(t *testing.T) {
	s, r, verifier := setup(t)
	ctx := t.Context()
	code, err := s.Authorize(ctx, 1, r, r.Scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []struct{ client, redirect, verifier string }{{"other", r.Redirect, verifier}, {r.ClientID, "http://127.0.0.1:4567/other", verifier}, {r.ClientID, r.Redirect, strings.Repeat("b", 43)}} {
		if _, err := s.Exchange(ctx, bad.client, code, bad.redirect, bad.verifier); err == nil {
			t.Fatal("invalid code binding accepted")
		}
	}
	tokens, err := s.Exchange(ctx, r.ClientID, code, r.Redirect, verifier)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exchange(ctx, r.ClientID, code, r.Redirect, verifier); err == nil {
		t.Fatal("authorization code reused")
	}
	id, err := s.ValidateToken(ctx, tokens.Access)
	if err != nil || id.UserID != 1 || !Has(id.Scope, "media:read") {
		t.Fatalf("identity %v %v", id, err)
	}
	if _, err := s.Refresh(ctx, "other", tokens.Refresh); err == nil {
		t.Fatal("refresh client mismatch accepted")
	}
	fresh, err := s.Refresh(ctx, r.ClientID, tokens.Refresh)
	if err != nil || fresh.Refresh == tokens.Refresh {
		t.Fatalf("refresh %v", err)
	}
	if _, err := s.Refresh(ctx, r.ClientID, tokens.Refresh); err == nil {
		t.Fatal("refresh replay accepted")
	}
	if _, err := s.ValidateToken(ctx, fresh.Access); err == nil {
		t.Fatal("refresh replay did not revoke family")
	}
}
func TestScopeConsentExpiryAndRevocation(t *testing.T) {
	s, r, v := setup(t)
	ctx := t.Context()
	if _, err := s.Authorize(ctx, 1, r, "media:delete"); err == nil {
		t.Fatal("scope escalation accepted")
	}
	bad := r
	bad.Resource = "https://other.example/mcp"
	if _, err := s.Validate(ctx, bad); err == nil {
		t.Fatal("wrong resource accepted")
	}
	bad = r
	bad.Redirect = "https://evil.example/"
	if _, err := s.Validate(ctx, bad); err == nil {
		t.Fatal("unregistered redirect accepted")
	}
	code, err := s.Authorize(ctx, 1, r, "media:read")
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.Exchange(ctx, r.ClientID, code, r.Redirect, v)
	if err != nil || token.Refresh != "" {
		t.Fatalf("read-only exchange: %v", err)
	}
	id, err := s.ValidateToken(ctx, token.Access)
	if err != nil {
		t.Fatal(err)
	}
	if Has(id.Scope, "media:request") {
		t.Fatal("consent scopes not enforced")
	}
	if err := s.Revoke(ctx, 2, id.GrantID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ValidateToken(ctx, token.Access); err != nil {
		t.Fatal("another user revoked grant")
	}
	if err := s.Revoke(ctx, 1, id.GrantID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ValidateToken(ctx, token.Access); err == nil {
		t.Fatal("revoked access token accepted")
	}
	code, err = s.Authorize(ctx, 1, r, r.Scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.ExecContext(t.Context(), `UPDATE oauth_codes SET expires_at=?`, time.Now().Add(-time.Second).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Exchange(ctx, r.ClientID, code, r.Redirect, v); err == nil {
		t.Fatal("expired code accepted")
	}
}
func TestMetadataRejectsPrivateAddresses(t *testing.T) {
	s, _, _ := setup(t)
	for _, id := range []string{"https://127.0.0.1/client.json", "https://[::1]/client.json", "http://example.com/client.json", "https://169.254.169.254/metadata", "https://localhost/client.json"} {
		if _, err := s.Client(t.Context(), id); err == nil {
			t.Errorf("accepted %s", id)
		}
	}
}

func TestClientMetadataAddressPolicy(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "0.0.0.1", "198.18.0.1", "::1", "::ffff:127.0.0.1", "fc00::1", "2001:db8::1"} {
		if publicIP(net.ParseIP(ip)) {
			t.Errorf("private/reserved address accepted: %s", ip)
		}
	}
	if !publicIP(net.ParseIP("1.1.1.1")) {
		t.Fatal("public address rejected")
	}
}
