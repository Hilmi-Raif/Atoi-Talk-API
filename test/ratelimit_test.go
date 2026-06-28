package test

import (
	"AtoiTalkAPI/ent/user"
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/model"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRateLimit_Public(t *testing.T) {
	clearDatabase(context.Background())

	reqBody := model.SendOTPRequest{
		Email:        "ratelimit@example.com",
		Mode:         "register",
		CaptchaToken: dummyTurnstileToken,
	}
	body, _ := json.Marshal(reqBody)

	for i := 0; i < 10; i++ {
		req, _ := http.NewRequest("POST", "/api/otp/send", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")

		req.Header.Set("X-Forwarded-For", "192.168.1.100")

		reqBody.Email = fmt.Sprintf("ratelimit%d@example.com", i)
		newBody, _ := json.Marshal(reqBody)
		req, _ = http.NewRequest("POST", "/api/otp/send", bytes.NewBuffer(newBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", "192.168.1.100")

		rr := executeRequest(req)
		assert.NotEqual(t, http.StatusTooManyRequests, rr.Code, fmt.Sprintf("Request %d should be allowed", i+1))
	}

	reqBody.Email = "ratelimit_final@example.com"
	newBody, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/api/otp/send", bytes.NewBuffer(newBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", "192.168.1.100")

	rr := executeRequest(req)
	assert.Equal(t, http.StatusTooManyRequests, rr.Code, "11th request should be blocked by IP rate limit")

	assert.Equal(t, "10", rr.Header().Get("X-RateLimit-Limit"))
	assert.Equal(t, "0", rr.Header().Get("X-RateLimit-Remaining"))
}

func TestRateLimit_Authenticated(t *testing.T) {

	clearDatabase(context.Background())

	u := createTestUser(t, "ratelimituser")
	token, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u.ID)

	for i := 0; i < 20; i++ {
		req := newUploadMediaRequest("message_attachment", "test.txt", 12, "text/plain", "dummy-token")
		req.Header.Set("Authorization", "Bearer "+token)

		req.Header.Set("X-Forwarded-For", fmt.Sprintf("192.168.1.%d", i))

		rr := executeRequest(req)
		assert.NotEqual(t, http.StatusTooManyRequests, rr.Code, fmt.Sprintf("Request %d should be allowed", i+1))
	}

	req := newUploadMediaRequest("message_attachment", "test.txt", 12, "text/plain", "dummy-token")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Forwarded-For", "192.168.1.99")

	rr := executeRequest(req)
	assert.Equal(t, http.StatusTooManyRequests, rr.Code, "21st request should be blocked by User ID rate limit")

	assert.Equal(t, "20", rr.Header().Get("X-RateLimit-Limit"))
	assert.Equal(t, "0", rr.Header().Get("X-RateLimit-Remaining"))
}

func TestRateLimit_Admin(t *testing.T) {
	clearDatabase(context.Background())

	admin := createTestUser(t, "adminrate")
	testClient.User.UpdateOne(admin).SetRole(user.RoleAdmin).ExecX(context.Background())
	token, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, admin.ID)

	req, _ := http.NewRequest("GET", "/api/admin/reports", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	rr := executeRequest(req)
	assert.Equal(t, http.StatusOK, rr.Code)

	limitHeader := rr.Header().Get("X-RateLimit-Limit")
	assert.Equal(t, "1000", limitHeader, "Admin should have a rate limit of 1000")
}
