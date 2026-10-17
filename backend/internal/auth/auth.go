package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"

	"lyrics/internal/domain"
	"lyrics/internal/ports"

	"golang.org/x/crypto/bcrypt"
)

type contextKey string

const userKey contextKey = "authenticated-user"

var ErrInvalidCredentials = errors.New("credenciales inválidas")

type Service struct {
	store ports.AuthRepository
}

func NewService(store ports.AuthRepository) *Service { return &Service{store: store} }

func (service *Service) Register(ctx context.Context, username, email, password string) (domain.User, string, error) {
	if len(username) < 3 || len(email) < 3 || len(password) < 8 {
		return domain.User{}, "", errors.New("usuario, email y contraseña válida son obligatorios")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return domain.User{}, "", err
	}
	user, err := service.store.CreateUser(ctx, strings.TrimSpace(username), strings.TrimSpace(email), string(hash))
	if err != nil {
		return domain.User{}, "", err
	}
	token, err := service.createToken(ctx, user.ID)
	return user, token, err
}

func (service *Service) Login(ctx context.Context, login, password string) (domain.User, string, error) {
	record, err := service.store.FindUserByLogin(ctx, strings.TrimSpace(login))
	if err != nil || bcrypt.CompareHashAndPassword([]byte(record.PasswordHash), []byte(password)) != nil {
		return domain.User{}, "", ErrInvalidCredentials
	}
	token, err := service.createToken(ctx, record.ID)
	return record.User, token, err
}

func (service *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return service.store.DeleteSession(ctx, hashToken(token))
}

func TokenFromRequest(request *http.Request) string { return bearerToken(request) }

func (service *Service) createToken(ctx context.Context, userID int64) (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(bytes)
	return token, service.store.CreateSession(ctx, hashToken(token), userID)
}

func (service *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		token := bearerToken(request)
		if token == "" {
			writeUnauthorized(writer)
			return
		}
		user, err := service.store.UserBySession(request.Context(), hashToken(token))
		if err != nil {
			writeUnauthorized(writer)
			return
		}
		next.ServeHTTP(writer, request.WithContext(context.WithValue(request.Context(), userKey, user)))
	})
}

func UserFromContext(ctx context.Context) (domain.User, bool) {
	user, ok := ctx.Value(userKey).(domain.User)
	return user, ok
}

func bearerToken(request *http.Request) string {
	parts := strings.Fields(request.Header.Get("Authorization"))
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return parts[1]
	}
	return ""
}

func hashToken(token string) string {
	digest := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func writeUnauthorized(writer http.ResponseWriter) {
	http.Error(writer, `{"error":"autenticación requerida"}`, http.StatusUnauthorized)
}
