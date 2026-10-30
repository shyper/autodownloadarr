// Package oauth implements user-delegated authorization for the MCP resource.
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

var ErrInvalid = errors.New("invalid OAuth request")
var ErrToken = errors.New("invalid or expired token")
var Scopes = []string{"media:read", "media:request", "media:manage", "media:delete"}

type Service struct {
	DB       *sql.DB
	Resource string
	HTTP     *http.Client
}
type Client struct {
	ID        string   `json:"client_id"`
	Name      string   `json:"client_name"`
	Redirects []string `json:"redirect_uris"`
}
type Request struct {
	ClientID  string
	Redirect  string
	Challenge string
	Scope     string
	State     string
	Resource  string
}
type Grant struct {
	ID         string `json:"id"`
	ClientID   string `json:"clientId"`
	ClientName string `json:"clientName"`
	Scopes     string `json:"scopes"`
	CreatedAt  int64  `json:"createdAt"`
}
type Identity struct {
	UserID  int64
	Scope   string
	GrantID string
}
type Tokens struct {
	Access  string `json:"access_token"`
	Type    string `json:"token_type"`
	Expires int    `json:"expires_in"`
	Refresh string `json:"refresh_token,omitempty"`
	Scope   string `json:"scope"`
}

func New(db *sql.DB, base string) *Service {
	// Client metadata is fetched without cookies or credentials and never from a
	// private network, including after DNS resolution. Redirects are not followed.
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	tr := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, ErrInvalid
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, ErrInvalid
		}
		for _, ip := range ips {
			if !publicIP(ip.IP) {
				return nil, ErrInvalid
			}
		}
		if len(ips) == 0 {
			return nil, ErrInvalid
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}}
	return &Service{DB: db, Resource: strings.TrimRight(base, "/") + "/mcp", HTTP: &http.Client{Transport: tr, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return ErrInvalid }}}
}
func random() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func hash(v string) []byte { h := sha256.Sum256([]byte(v)); return h[:] }
func Has(scope, required string) bool {
	for _, s := range strings.Fields(scope) {
		if s == required {
			return true
		}
	}
	return false
}
func validScopes(scope string) bool {
	if len(strings.Fields(scope)) == 0 {
		return false
	}
	for _, s := range strings.Fields(scope) {
		if s != "offline_access" && !Has(strings.Join(Scopes, " "), s) {
			return false
		}
	}
	return true
}
func ValidRedirect(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Fragment != "" || u.Host == "" {
		return false
	}
	return u.Scheme == "https" || (u.Scheme == "http" && (u.Hostname() == "localhost" || net.ParseIP(u.Hostname()).IsLoopback()))
}
func (s *Service) SaveClient(ctx context.Context, c Client) (Client, error) {
	if len(c.Name) < 1 || len(c.Name) > 100 || len(c.Redirects) < 1 || len(c.Redirects) > 10 {
		return c, ErrInvalid
	}
	for _, u := range c.Redirects {
		if !ValidRedirect(u) {
			return c, ErrInvalid
		}
	}
	c.ID = random()
	data, _ := json.Marshal(c.Redirects)
	_, err := s.DB.ExecContext(ctx, `INSERT INTO oauth_clients(id,name,redirect_uris) VALUES(?,?,?)`, c.ID, c.Name, string(data))
	return c, err
}
func (s *Service) Client(ctx context.Context, id string) (Client, error) {
	var c Client
	var redirects string
	err := s.DB.QueryRowContext(ctx, `SELECT id,name,redirect_uris FROM oauth_clients WHERE id=?`, id).Scan(&c.ID, &c.Name, &redirects)
	if err == nil {
		err = json.Unmarshal([]byte(redirects), &c.Redirects)
		return c, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return c, err
	}
	u, e := url.Parse(id)
	if e != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.Host == "" || u.Path == "" || u.Path == "/" {
		return c, ErrInvalid
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, id, nil)
	resp, e := s.HTTP.Do(req)
	if e != nil {
		return c, ErrInvalid
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return c, ErrInvalid
	}
	// Metadata documents are public, bounded JSON, with an exact client identifier.
	dec := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 64<<10))
	if dec.Decode(&c) != nil || c.ID != id || c.Name == "" || len(c.Name) > 100 {
		return c, ErrInvalid
	}
	return c, nil
}
func (s *Service) Validate(ctx context.Context, r Request) (Client, error) {
	c, err := s.Client(ctx, r.ClientID)
	if err != nil {
		return c, ErrInvalid
	}
	match := false
	for _, redirect := range c.Redirects {
		if redirect == r.Redirect && ValidRedirect(redirect) {
			match = true
		}
	}
	challenge, err := base64.RawURLEncoding.DecodeString(r.Challenge)
	if !match || err != nil || len(challenge) != 32 || !validScopes(r.Scope) || len(r.State) > 2048 || (r.Resource != "" && r.Resource != s.Resource) {
		return c, ErrInvalid
	}
	return c, nil
}
func (s *Service) Authorize(ctx context.Context, userID int64, r Request, scope string) (string, error) {
	c, err := s.Validate(ctx, r)
	if err != nil || !validScopes(scope) {
		return "", ErrInvalid
	}
	for _, v := range strings.Fields(scope) {
		if !Has(r.Scope, v) {
			return "", ErrInvalid
		}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	grant, code := random(), random()
	if _, err = tx.ExecContext(ctx, `INSERT INTO oauth_grants(id,user_id,client_id,client_name,scopes,created_at) VALUES(?,?,?,?,?,?)`, grant, userID, c.ID, c.Name, scope, time.Now().UnixMilli()); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO oauth_codes(hash,grant_id,redirect_uri,challenge,expires_at) VALUES(?,?,?,?,?)`, hash(code), grant, r.Redirect, r.Challenge, time.Now().Add(5*time.Minute).UnixMilli()); err != nil {
		return "", err
	}
	return code, tx.Commit()
}
func (s *Service) issue(ctx context.Context, tx *sql.Tx, grant, scope string) (Tokens, error) {
	t := Tokens{Access: random(), Type: "Bearer", Expires: 600, Scope: scope}
	if _, err := tx.ExecContext(ctx, `INSERT INTO oauth_tokens(hash,grant_id,kind,expires_at) VALUES(?,?,'access',?)`, hash(t.Access), grant, time.Now().Add(10*time.Minute).UnixMilli()); err != nil {
		return t, err
	}
	if Has(scope, "offline_access") {
		t.Refresh = random()
		if _, err := tx.ExecContext(ctx, `INSERT INTO oauth_tokens(hash,grant_id,kind,expires_at) VALUES(?,?,'refresh',?)`, hash(t.Refresh), grant, time.Now().Add(30*24*time.Hour).UnixMilli()); err != nil {
			return t, err
		}
	}
	return t, nil
}
func (s *Service) Exchange(ctx context.Context, clientID, code, redirect, verifier string) (Tokens, error) {
	if len(verifier) < 43 || len(verifier) > 128 || strings.Trim(verifier, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~") != "" {
		return Tokens{}, ErrToken
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Tokens{}, err
	}
	defer tx.Rollback()
	var grant, challenge, scope string
	err = tx.QueryRowContext(ctx, `SELECT c.grant_id,c.challenge,g.scopes FROM oauth_codes c JOIN oauth_grants g ON g.id=c.grant_id WHERE c.hash=? AND c.redirect_uri=? AND c.expires_at>? AND g.client_id=? AND g.revoked=0`, hash(code), redirect, time.Now().UnixMilli(), clientID).Scan(&grant, &challenge, &scope)
	if err != nil || base64.RawURLEncoding.EncodeToString(hash(verifier)) != challenge {
		return Tokens{}, ErrToken
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM oauth_codes WHERE hash=?`, hash(code)); err != nil {
		return Tokens{}, err
	}
	t, err := s.issue(ctx, tx, grant, scope)
	if err != nil {
		return t, err
	}
	return t, tx.Commit()
}
func (s *Service) Refresh(ctx context.Context, clientID, token string) (Tokens, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Tokens{}, err
	}
	defer tx.Rollback()
	var grant, scope string
	var used int
	err = tx.QueryRowContext(ctx, `SELECT t.grant_id,g.scopes,t.used FROM oauth_tokens t JOIN oauth_grants g ON g.id=t.grant_id WHERE t.hash=? AND t.kind='refresh' AND t.expires_at>? AND g.client_id=? AND g.revoked=0`, hash(token), time.Now().UnixMilli(), clientID).Scan(&grant, &scope, &used)
	if err != nil {
		return Tokens{}, ErrToken
	}
	if used != 0 {
		_, err = tx.ExecContext(ctx, `UPDATE oauth_grants SET revoked=1 WHERE id=?`, grant)
		if err != nil {
			return Tokens{}, err
		}
		if err = tx.Commit(); err != nil {
			return Tokens{}, err
		}
		return Tokens{}, ErrToken
	}
	if _, err = tx.ExecContext(ctx, `UPDATE oauth_tokens SET used=1 WHERE hash=?`, hash(token)); err != nil {
		return Tokens{}, err
	}
	t, err := s.issue(ctx, tx, grant, scope)
	if err != nil {
		return t, err
	}
	return t, tx.Commit()
}
func (s *Service) ValidateToken(ctx context.Context, token string) (Identity, error) {
	var i Identity
	err := s.DB.QueryRowContext(ctx, `SELECT g.user_id,g.scopes,g.id FROM oauth_tokens t JOIN oauth_grants g ON g.id=t.grant_id WHERE t.hash=? AND t.kind='access' AND t.expires_at>? AND g.revoked=0`, hash(token), time.Now().UnixMilli()).Scan(&i.UserID, &i.Scope, &i.GrantID)
	if errors.Is(err, sql.ErrNoRows) {
		return i, ErrToken
	}
	return i, err
}
func (s *Service) Grants(ctx context.Context, userID int64) ([]Grant, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,client_id,client_name,scopes,created_at FROM oauth_grants WHERE user_id=? AND revoked=0 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Grant{}
	for rows.Next() {
		var g Grant
		if err := rows.Scan(&g.ID, &g.ClientID, &g.ClientName, &g.Scopes, &g.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, g)
	}
	return list, rows.Err()
}
func (s *Service) Revoke(ctx context.Context, userID int64, id string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE oauth_grants SET revoked=1 WHERE id=? AND user_id=?`, id, userID)
	return err
}
func (s *Service) RevokeToken(ctx context.Context, clientID, token string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE oauth_grants SET revoked=1 WHERE client_id=? AND id IN (SELECT grant_id FROM oauth_tokens WHERE hash=?)`, clientID, hash(token))
	return err
}

func publicIP(ip net.IP) bool {
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	address = address.Unmap()
	for _, cidr := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32"} {
		if netip.MustParsePrefix(cidr).Contains(address) {
			return false
		}
	}
	return true
}
