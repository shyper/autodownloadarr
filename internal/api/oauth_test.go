package api

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/shyper/autodownloadarr/internal/oauth"
)

func TestMCPDelegationScopesAndRevocation(t *testing.T) {
	e := newEnv(t)
	client := e.client(t)
	e.completeSetup(t, client)
	c, err := e.oauth.SaveClient(t.Context(), oauth.Client{Name: "Test", Redirects: []string{"http://localhost:4567/callback"}})
	if err != nil {
		t.Fatal(err)
	}
	verifier := strings.Repeat("a", 43)
	challenge := sha256.Sum256([]byte(verifier))
	code, err := e.oauth.Authorize(t.Context(), 1, oauth.Request{ClientID: c.ID, Redirect: c.Redirects[0], Challenge: base64.RawURLEncoding.EncodeToString(challenge[:]), Scope: "media:read"}, "media:read")
	if err != nil {
		t.Fatal(err)
	}
	token, err := e.oauth.Exchange(t.Context(), c.ID, code, c.Redirects[0], verifier)
	if err != nil {
		t.Fatal(err)
	}
	call := func(method string, params any) (result, map[string]any) {
		return e.do(t, client, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params}, "Authorization", "Bearer "+token.Access, "Accept", "application/json, text/event-stream", "MCP-Protocol-Version", "2025-11-25")
	}
	res, body := call("initialize", map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "test", "version": "1"}})
	if res.StatusCode != 200 || body["result"] == nil {
		t.Fatalf("initialize %d %v", res.StatusCode, body)
	}
	res, body = call("tools/list", map[string]any{})
	if res.StatusCode != 200 {
		t.Fatalf("tools/list %d %v", res.StatusCode, body)
	}
	tools := body["result"].(map[string]any)["tools"].([]any)
	for _, tool := range tools {
		name := tool.(map[string]any)["name"].(string)
		if strings.Contains(name, "delete") || strings.HasPrefix(name, "request") {
			t.Fatalf("write tool in read grant: %s", name)
		}
	}
	res, body = call("tools/call", map[string]any{"name": "list_movies", "arguments": map[string]any{}})
	if res.StatusCode != 200 || body["result"] == nil {
		t.Fatalf("read tool %d %v", res.StatusCode, body)
	}
	if err := e.oauth.RevokeToken(t.Context(), c.ID, token.Access); err != nil {
		t.Fatal(err)
	}
	res, _ = call("tools/list", map[string]any{})
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked token accepted: %d", res.StatusCode)
	}
}
func TestOAuthConsentRequiresSessionAndPKCE(t *testing.T) {
	e := newEnv(t)
	client := e.client(t)
	e.completeSetup(t, client)
	c, err := e.oauth.SaveClient(t.Context(), oauth.Client{Name: "Test", Redirects: []string{"http://localhost:4567/callback"}})
	if err != nil {
		t.Fatal(err)
	}
	challenge := sha256.Sum256([]byte(strings.Repeat("a", 43)))
	path := fmt.Sprintf("/api/v1/oauth/authorization?client_id=%s&redirect_uri=http%%3A%%2F%%2Flocalhost%%3A4567%%2Fcallback&scope=media%%3Aread&response_type=code&code_challenge_method=S256&code_challenge=%s", c.ID, base64.RawURLEncoding.EncodeToString(challenge[:]))
	res, _ := e.do(t, e.client(t), "GET", path, nil)
	if res.StatusCode != 401 {
		t.Fatal("anonymous consent accepted")
	}
	res, body := e.do(t, client, "GET", path, nil)
	if res.StatusCode != 200 || body["clientName"] != "Test" {
		t.Fatalf("consent %d %v", res.StatusCode, body)
	}
	res, _ = e.do(t, client, "GET", strings.Replace(path, "S256", "plain", 1), nil)
	if res.StatusCode != 400 {
		t.Fatal("plain PKCE accepted")
	}
}
