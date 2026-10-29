package auth

import (
	"context"
	"errors"
	"testing"
)

func TestUserManagement(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()
	admin, err := svc.CreateInitialAdmin(ctx, "admin", "longenough")
	if err != nil {
		t.Fatal(err)
	}

	alice, err := svc.CreateUser(ctx, "Alice", "longenough", RoleUser)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if alice.Role != RoleUser || alice.Username != "Alice" {
		t.Errorf("unexpected user %+v", alice)
	}
	if _, err := svc.CreateUser(ctx, "alice", "longenough", RoleUser); !errors.Is(err, ErrUserExists) {
		t.Errorf("duplicate username: got %v", err)
	}
	if _, err := svc.CreateUser(ctx, "bad name!", "longenough", RoleUser); !errors.Is(err, ErrInvalidUsername) {
		t.Errorf("invalid username: got %v", err)
	}
	if _, err := svc.CreateUser(ctx, "bob", "short", RoleUser); !errors.Is(err, ErrWeakPassword) {
		t.Errorf("weak password: got %v", err)
	}
	if _, err := svc.CreateUser(ctx, "bob", "longenough", Role("owner")); !errors.Is(err, ErrInvalidRole) {
		t.Errorf("invalid role: got %v", err)
	}

	users, err := svc.Users(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 || users[0].Username != "admin" || users[1].Username != "Alice" {
		t.Errorf("Users = %+v", users)
	}

	// The last administrator cannot be demoted or deleted.
	if _, err := svc.UpdateRole(ctx, admin.ID, RoleUser); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("demote last admin: got %v", err)
	}
	if err := svc.DeleteUser(ctx, admin.ID); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("delete last admin: got %v", err)
	}

	// With a second administrator the first one may step down.
	if _, err := svc.UpdateRole(ctx, alice.ID, RoleAdmin); err != nil {
		t.Fatalf("promote: %v", err)
	}
	if _, err := svc.UpdateRole(ctx, admin.ID, RoleUser); err != nil {
		t.Fatalf("demote: %v", err)
	}
	if u, err := svc.User(ctx, admin.ID); err != nil || u.Role != RoleUser {
		t.Errorf("admin role after demotion = %+v, %v", u, err)
	}
	if _, err := svc.User(ctx, 999); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("missing user: got %v", err)
	}
	if _, err := svc.UpdateRole(ctx, 999, RoleUser); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("update missing user: got %v", err)
	}

	// A password reset ends the user's sessions and lets them sign in again.
	token, _, err := svc.CreateSession(ctx, alice.ID, "agent", "192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SetUserPassword(ctx, alice.ID, "newpassword"); err != nil {
		t.Fatalf("SetUserPassword: %v", err)
	}
	if _, _, err := svc.ValidateSession(ctx, token); !errors.Is(err, ErrInvalidSession) {
		t.Errorf("session after reset: got %v", err)
	}
	if _, err := svc.Authenticate(ctx, "alice", "longenough"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("old password still works: got %v", err)
	}
	if _, err := svc.Authenticate(ctx, "alice", "newpassword"); err != nil {
		t.Errorf("new password rejected: %v", err)
	}

	// Alice is the only administrator now, so she cannot be deleted.
	if err := svc.DeleteUser(ctx, alice.ID); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("delete last admin: got %v", err)
	}
	if _, err := svc.UpdateRole(ctx, admin.ID, RoleAdmin); err != nil {
		t.Fatalf("restore admin: %v", err)
	}
	if err := svc.DeleteUser(ctx, alice.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if _, err := svc.User(ctx, alice.ID); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("deleted user: got %v", err)
	}
}
