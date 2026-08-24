//go:build integration

package integration

import (
	"AtoiTalkAPI/internal/helper"
	"context"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
)

func TestSessionRevoke_AllowsNewTokenWithIatMillisInSameSecond(t *testing.T) {
	clearDatabase(context.Background())

	u := createTestUser(t, "revokeprecision")

	issuedAtSecond := time.Now().UTC().Truncate(time.Second)
	revokedAt := issuedAtSecond.UnixMilli() + 500
	tokenIssuedAtMillis := revokedAt + 1

	revokeKey := fmt.Sprintf("revoked_user:%s", u.ID)
	err := redisAdapter.Set(
		context.Background(),
		revokeKey,
		strconv.FormatInt(revokedAt, 10),
		time.Duration(testConfig.JWTExp)*time.Second,
	)
	if !assert.NoError(t, err) {
		return
	}

	claims := helper.JWTClaims{
		UserID:         u.ID,
		IssuedAtMillis: tokenIssuedAtMillis,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(issuedAtSecond),
			ExpiresAt: jwt.NewNumericDate(issuedAtSecond.Add(5 * time.Minute)),
			Issuer:    "AtoiTalkAPI",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(testConfig.JWTSecret))
	if !assert.NoError(t, err) {
		return
	}

	rr := makeRequest(http.MethodGet, "/api/user/current", nil, tokenString)
	assert.Equal(t, http.StatusOK, rr.Code)
}
