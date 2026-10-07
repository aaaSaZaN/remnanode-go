package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/remnawave/node-go/internal/config"
)

func TestJWTMiddleware(t *testing.T) {
	// test RSA key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}

	pubASN1, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatalf("failed to marshal public key: %v", err)
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubASN1,
	})

	cfg := &config.Config{
		Payload: &config.NodePayload{
			JWTPublicKey: string(pubPEM),
		},
		InternalRestToken: "test-token",
	}

	authMgr, err := NewAuthManager(cfg)
	if err != nil {
		t.Fatalf("failed to create auth manager: %v", err)
	}

	// make a test handler
	handlerCalled := false
	testHandler := authMgr.JWTMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	// no authorization header
	req := httptest.NewRequest("GET", "/node/stats/get-system-stats", nil)
	rr := httptest.NewRecorder()
	testHandler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got %d", rr.Code)
	}
	if handlerCalled {
		t.Fatal("handler should not have been called without token")
	}

	// valid jwt token
	claims := jwt.MapClaims{
		"sub": "remnawave-panel",
		"exp": time.Now().Add(1 * time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tokenStr, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatalf("failed to sign token: %v", err)
	}

	req = httptest.NewRequest("GET", "/node/stats/get-system-stats", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	rr = httptest.NewRecorder()
	testHandler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rr.Code)
	}
	if !handlerCalled {
		t.Fatal("handler should have been called with valid token")
	}

	// invalid jwt
	req = httptest.NewRequest("GET", "/node/stats/get-system-stats", nil)
	req.Header.Set("Authorization", "Bearer invalid.token.payload")
	rr = httptest.NewRecorder()
	handlerCalled = false
	testHandler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got %d", rr.Code)
	}
	if handlerCalled {
		t.Fatal("handler should not have been called with invalid token")
	}
}

func TestInternalTokenMiddleware(t *testing.T) {
	cfg := &config.Config{
		InternalRestToken: "my-internal-secret",
		Payload:           &config.NodePayload{},
	}
	authMgr := &AuthManager{
		internalRestToken: cfg.InternalRestToken,
	}

	handlerCalled := false
	testHandler := authMgr.InternalTokenMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	// wrong token
	req := httptest.NewRequest("GET", "/internal/get-config?token=wrong", nil)
	rr := httptest.NewRecorder()
	testHandler.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden, got %d", rr.Code)
	}

	// ok token
	req = httptest.NewRequest("GET", "/internal/get-config?token=my-internal-secret", nil)
	rr = httptest.NewRecorder()
	testHandler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rr.Code)
	}
	if !handlerCalled {
		t.Fatal("handler should have been called with valid internal token")
	}
}
