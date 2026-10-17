package auth

import (
	"context"
	"path/filepath"
	"testing"

	"lyrics/internal/storage"
)

func TestRegisterLoginAndSession(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	service := NewService(store)
	user, token, err := service.Register(context.Background(), "tester", "tester@example.com", "password123")
	if err != nil || user.ID == 0 || token == "" {
		t.Fatalf("Register() = (%#v, %q, %v)", user, token, err)
	}

	loggedUser, loginToken, err := service.Login(context.Background(), "tester", "password123")
	if err != nil || loggedUser.ID != user.ID || loginToken == "" {
		t.Fatalf("Login() = (%#v, %q, %v)", loggedUser, loginToken, err)
	}
	if _, _, err := service.Login(context.Background(), "tester", "wrong-password"); err != ErrInvalidCredentials {
		t.Fatalf("invalid login error = %v, want ErrInvalidCredentials", err)
	}

	if err := service.Logout(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UserBySession(context.Background(), hashToken(token)); err == nil {
		t.Fatal("logged out token still resolves to a user")
	}
}
