package helper

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type JWTClaims struct {
	UserID         uuid.UUID `json:"user_id"`
	IssuedAtMillis int64     `json:"iat_ms,omitempty"`
	jwt.RegisteredClaims
}

func GenerateJWT(jwtSecret string, jwtExp int, userID uuid.UUID) (string, error) {
	now := time.Now().UTC()

	claims := JWTClaims{
		UserID:         userID,
		IssuedAtMillis: now.UnixMilli(),
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Duration(jwtExp) * time.Second)),
			IssuedAt:  jwt.NewNumericDate(now),
			Issuer:    "AtoiTalkAPI",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signedToken, err := token.SignedString([]byte(jwtSecret))
	if err != nil {
		return "", fmt.Errorf("failed to sign token: %w", err)
	}

	return signedToken, nil
}
