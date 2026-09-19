package helper

import (
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func TestGenerateJWTContainsExpectedClaims(t *testing.T) {
	userID := uuid.New()
	tokenString, err := GenerateJWT("secret", 60, userID)
	if err != nil {
		t.Fatal(err)
	}
	token, err := jwt.ParseWithClaims(tokenString, &JWTClaims{}, func(token *jwt.Token) (interface{}, error) {
		if token.Method != jwt.SigningMethodHS256 {
			t.Fatalf("unexpected signing method: %v", token.Method)
		}
		return []byte("secret"), nil
	})
	if err != nil || !token.Valid {
		t.Fatalf("invalid generated JWT: valid=%v err=%v", token.Valid, err)
	}
	claims, ok := token.Claims.(*JWTClaims)
	if !ok || claims.UserID != userID || claims.Issuer != "AtoiTalkAPI" {
		t.Fatalf("unexpected claims: %#v", token.Claims)
	}
}
