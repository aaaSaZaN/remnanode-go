package auth

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/remnawave/node-go/internal/config"
)

type AuthManager struct {
	rsaPubKey         *rsa.PublicKey
	internalRestToken string
}

func NewAuthManager(cfg *config.Config) (*AuthManager, error) {
	block, _ := pem.Decode([]byte(cfg.Payload.JWTPublicKey))
	if block == nil {
		return nil, errors.New("failed to decode JWT public key PEM")
	}

	var rsaKey *rsa.PublicKey
	if pub, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		if k, ok := pub.(*rsa.PublicKey); ok {
			rsaKey = k
		}
	} else if pub, err := x509.ParsePKCS1PublicKey(block.Bytes); err == nil {
		rsaKey = pub
	}

	if rsaKey == nil {
		return nil, errors.New("JWT public key is not a valid RSA public key")
	}

	return &AuthManager{
		rsaPubKey:         rsaKey,
		internalRestToken: cfg.InternalRestToken,
	}, nil
}

func (a *AuthManager) JWTMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			log.Printf("[AUTH] Missing or invalid Authorization header from %s", r.RemoteAddr)
			writeUnauthorized(w)
			return
		}

		tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
		token, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			return a.rsaPubKey, nil
		})

		if err != nil || !token.Valid {
			log.Printf("[AUTH] Invalid JWT token from %s: %v", r.RemoteAddr, err)
			writeUnauthorized(w)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (a *AuthManager) InternalTokenMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("token")
		if token == "" || token != a.internalRestToken {
			log.Printf("[AUTH] Internal token mismatch or missing from %s", r.RemoteAddr)
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"statusCode": 401,
		"message":    "Unauthorized",
	})
}
