package api

import (
	"fmt"
	"net/http"
	"testing"
)

func TestUserManagementAPI(t *testing.T) {
	e := newEnv(t)
	c := e.client(t)
	e.completeSetup(t, c)

	listUsers := func(c *http.Client) ([]map[string]any, int) {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, e.srv.URL+"/api/v1/users", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var users []map[string]any
		_ = decodeBody(resp, &users)
		return users, resp.StatusCode
	}

	users, status := listUsers(c)
	if status != http.StatusOK || len(users) != 1 || users[0]["role"] != "admin" {
		t.Fatalf("initial users: %d %v", status, users)
	}
	adminID := users[0]["id"]

	resp, body := e.do(t, c, "POST", "/api/v1/users", map[string]string{
		"username": "bob", "password": "longenough", "role": "user",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create user: %d %v", resp.StatusCode, body)
	}
	bobID := body["id"]
	if body["username"] != "bob" || body["role"] != "user" {
		t.Errorf("created user = %v", body)
	}

	resp, body = e.do(t, c, "POST", "/api/v1/users", map[string]string{
		"username": "bob", "password": "longenough", "role": "user",
	})
	if resp.StatusCode != http.StatusConflict || errorCode(body) != "username_taken" {
		t.Errorf("duplicate user: %d %v", resp.StatusCode, body)
	}
	resp, body = e.do(t, c, "POST", "/api/v1/users", map[string]string{
		"username": "eve", "password": "short", "role": "user",
	})
	if resp.StatusCode != http.StatusBadRequest || errorCode(body) != "invalid_input" {
		t.Errorf("weak password: %d %v", resp.StatusCode, body)
	}

	// The last administrator cannot be demoted.
	resp, body = e.do(t, c, "PUT", "/api/v1/users/"+itoa(adminID), map[string]string{"role": "user"})
	if resp.StatusCode != http.StatusConflict || errorCode(body) != "last_admin" {
		t.Errorf("demote last admin: %d %v", resp.StatusCode, body)
	}

	// A regular user may not see or change accounts.
	bob := e.client(t)
	if resp, _ := e.do(t, bob, "POST", "/api/v1/auth/login",
		map[string]string{"username": "bob", "password": "longenough"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("bob login: %d", resp.StatusCode)
	}
	if _, status := listUsers(bob); status != http.StatusForbidden {
		t.Errorf("non-admin list users: %d", status)
	}
	resp, body = e.do(t, bob, "POST", "/api/v1/users", map[string]string{
		"username": "eve", "password": "longenough", "role": "user",
	})
	if resp.StatusCode != http.StatusForbidden || errorCode(body) != "forbidden" {
		t.Errorf("non-admin create user: %d %v", resp.StatusCode, body)
	}

	// Promote, reset the password and remove the second account again.
	resp, body = e.do(t, c, "PUT", "/api/v1/users/"+itoa(bobID), map[string]any{
		"role": "admin", "quotaRequests": 5, "quotaGb": 10,
	})
	if resp.StatusCode != http.StatusOK || body["role"] != "admin" || body["quotaRequests"] != float64(5) || body["quotaGb"] != float64(10) {
		t.Fatalf("promote bob: %d %v", resp.StatusCode, body)
	}
	// Self-deletion is forbidden even when another administrator exists.
	resp, body = e.do(t, c, "DELETE", "/api/v1/users/"+itoa(adminID), nil)
	if resp.StatusCode != http.StatusConflict || errorCode(body) != "self_deletion" {
		t.Fatalf("self-deletion with another admin: %d %v", resp.StatusCode, body)
	}
	if resp, _ := e.do(t, c, "GET", "/api/v1/auth/me", nil); resp.StatusCode != http.StatusOK {
		t.Fatal("self-deletion invalidated the session")
	}
	if resp, _ := e.do(t, c, "POST", "/api/v1/users/"+itoa(bobID)+"/password",
		map[string]string{"newPassword": "newpassword"}); resp.StatusCode != http.StatusNoContent {
		t.Errorf("reset password: %d", resp.StatusCode)
	}
	if resp, _ := e.do(t, bob, "GET", "/api/v1/auth/me", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("session survived password reset: %d", resp.StatusCode)
	}
	if resp, _ := e.do(t, c, "DELETE", "/api/v1/users/"+itoa(bobID), nil); resp.StatusCode != http.StatusNoContent {
		t.Errorf("delete bob: %d", resp.StatusCode)
	}
	if users, _ := listUsers(c); len(users) != 1 {
		t.Errorf("users after delete: %v", users)
	}
	resp, body = e.do(t, c, "DELETE", "/api/v1/users/"+itoa(adminID), nil)
	if resp.StatusCode != http.StatusConflict || errorCode(body) != "self_deletion" {
		t.Errorf("delete last admin: %d %v", resp.StatusCode, body)
	}
}

func itoa(v any) string {
	return fmt.Sprint(uint64(v.(float64)))
}
