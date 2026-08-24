//go:build integration

package integration

import (
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/model"
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSendOTP(t *testing.T) {
	ctx := context.Background()

	t.Run("Success - New OTP", func(t *testing.T) {
		clearDatabase(ctx)
		originalSecret := testConfig.TurnstileSecretKey
		testConfig.TurnstileSecretKey = cfTurnstileAlwaysPasses
		defer func() { testConfig.TurnstileSecretKey = originalSecret }()

		reqBody := model.SendOTPRequest{
			Email:        "test-new@example.com",
			Mode:         "register",
			CaptchaToken: dummyTurnstileToken,
		}
		rr := makeRequest("POST", "/api/otp/send", reqBody, "")
		assert.Equal(t, http.StatusOK, rr.Code)

		key := fmt.Sprintf("otp:%s:%s", reqBody.Mode, reqBody.Email)
		val, err := redisAdapter.Get(ctx, key)
		assert.NoError(t, err)
		assert.NotEmpty(t, val)
	})

	t.Run("Success - Update Existing OTP", func(t *testing.T) {
		clearDatabase(ctx)
		originalSecret := testConfig.TurnstileSecretKey
		testConfig.TurnstileSecretKey = cfTurnstileAlwaysPasses
		defer func() { testConfig.TurnstileSecretKey = originalSecret }()

		email := "test-update@example.com"
		hashedPassword, _ := helper.HashPassword("Password123!")
		testClient.User.Create().
			SetEmail(email).
			SetUsername("testupdate").
			SetFullName("Existing User").
			SetPasswordHash(hashedPassword).
			Save(ctx)

		reqBody1 := model.SendOTPRequest{
			Email:        email,
			Mode:         "reset",
			CaptchaToken: dummyTurnstileToken,
		}
		makeRequest("POST", "/api/otp/send", reqBody1, "")

		key := fmt.Sprintf("otp:%s:%s", reqBody1.Mode, email)
		firstCode, err := redisAdapter.Get(ctx, key)
		if !assert.NoError(t, err) {
			return
		}

		time.Sleep(2 * time.Second)

		reqBody2 := model.SendOTPRequest{
			Email:        email,
			Mode:         "reset",
			CaptchaToken: dummyTurnstileToken,
		}
		rr := makeRequest("POST", "/api/otp/send", reqBody2, "")

		if rr.Code == http.StatusTooManyRequests {
			return
		}

		assert.Equal(t, http.StatusOK, rr.Code)

		secondCode, err := redisAdapter.Get(ctx, key)
		if !assert.NoError(t, err) {
			return
		}
		assert.NotEqual(t, firstCode, secondCode)
	})

	t.Run("Validation Error - Missing Email", func(t *testing.T) {
		clearDatabase(ctx)
		reqBody := model.SendOTPRequest{
			Mode:         "register",
			CaptchaToken: dummyTurnstileToken,
		}
		rr := makeRequest("POST", "/api/otp/send", reqBody, "")
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Validation Error - Invalid Mode", func(t *testing.T) {
		clearDatabase(ctx)
		reqBody := model.SendOTPRequest{
			Email:        "test-invalid-mode@example.com",
			Mode:         "invalid-mode",
			CaptchaToken: dummyTurnstileToken,
		}
		rr := makeRequest("POST", "/api/otp/send", reqBody, "")
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Rate Limit Error", func(t *testing.T) {
		clearDatabase(ctx)
		originalSecret := testConfig.TurnstileSecretKey
		testConfig.TurnstileSecretKey = cfTurnstileAlwaysPasses
		defer func() { testConfig.TurnstileSecretKey = originalSecret }()

		reqBody := model.SendOTPRequest{
			Email:        "ratelimit@example.com",
			Mode:         "register",
			CaptchaToken: dummyTurnstileToken,
		}

		for i := 0; i < 5; i++ {
			makeRequest("POST", "/api/otp/send", reqBody, "")
		}

		rr := makeRequest("POST", "/api/otp/send", reqBody, "")
		assert.Equal(t, http.StatusTooManyRequests, rr.Code)
		errStr := parseErrorResponse(t, rr)
		assert.Contains(t, errStr, "Please try again")
	})

	t.Run("Invalid Captcha Token", func(t *testing.T) {
		clearDatabase(ctx)
		originalSecret := testConfig.TurnstileSecretKey
		testConfig.TurnstileSecretKey = cfTurnstileAlwaysFails
		defer func() { testConfig.TurnstileSecretKey = originalSecret }()

		reqBody := model.SendOTPRequest{
			Email:        "test-invalid-captcha@example.com",
			Mode:         "register",
			CaptchaToken: dummyTurnstileToken,
		}
		rr := makeRequest("POST", "/api/otp/send", reqBody, "")
		assert.Equal(t, http.StatusBadRequest, rr.Code)
		errStr := parseErrorResponse(t, rr)
		assert.Equal(t, helper.MsgBadRequest, errStr)
	})

	t.Run("Captcha Token Already Spent", func(t *testing.T) {
		clearDatabase(ctx)
		originalSecret := testConfig.TurnstileSecretKey
		testConfig.TurnstileSecretKey = cfTurnstileTokenAlreadySpent
		defer func() { testConfig.TurnstileSecretKey = originalSecret }()

		reqBody := model.SendOTPRequest{
			Email:        "test-already-spent@example.com",
			Mode:         "register",
			CaptchaToken: dummyTurnstileToken,
		}
		rr := makeRequest("POST", "/api/otp/send", reqBody, "")
		assert.Equal(t, http.StatusBadRequest, rr.Code)
		errStr := parseErrorResponse(t, rr)
		assert.Equal(t, helper.MsgBadRequest, errStr)
	})

	t.Run("Silent Fail - Register Existing Email", func(t *testing.T) {
		clearDatabase(ctx)
		originalSecret := testConfig.TurnstileSecretKey
		testConfig.TurnstileSecretKey = cfTurnstileAlwaysPasses
		defer func() { testConfig.TurnstileSecretKey = originalSecret }()

		email := "existing-user@example.com"
		hashedPassword, _ := helper.HashPassword("Password123!")
		testClient.User.Create().
			SetEmail(email).
			SetUsername("existinguser").
			SetFullName("Existing User").
			SetPasswordHash(hashedPassword).
			Save(ctx)

		reqBody := model.SendOTPRequest{
			Email:        email,
			Mode:         "register",
			CaptchaToken: dummyTurnstileToken,
		}
		rr := makeRequest("POST", "/api/otp/send", reqBody, "")
		assert.Equal(t, http.StatusOK, rr.Code)

		key := fmt.Sprintf("otp:%s:%s", reqBody.Mode, reqBody.Email)
		_, err := redisAdapter.Get(ctx, key)
		assert.Error(t, err)
	})

	t.Run("Silent Fail - Reset Non-Existent Email", func(t *testing.T) {
		clearDatabase(ctx)
		originalSecret := testConfig.TurnstileSecretKey
		testConfig.TurnstileSecretKey = cfTurnstileAlwaysPasses
		defer func() { testConfig.TurnstileSecretKey = originalSecret }()

		reqBody := model.SendOTPRequest{
			Email:        "non-existent@example.com",
			Mode:         "reset",
			CaptchaToken: dummyTurnstileToken,
		}
		rr := makeRequest("POST", "/api/otp/send", reqBody, "")
		assert.Equal(t, http.StatusOK, rr.Code)

		key := fmt.Sprintf("otp:%s:%s", reqBody.Mode, reqBody.Email)
		_, err := redisAdapter.Get(ctx, key)
		assert.Error(t, err)
	})

	t.Run("Silent Fail - ChangeEmail to Existing Email", func(t *testing.T) {
		clearDatabase(ctx)
		originalSecret := testConfig.TurnstileSecretKey
		testConfig.TurnstileSecretKey = cfTurnstileAlwaysPasses
		defer func() { testConfig.TurnstileSecretKey = originalSecret }()

		hashedPassword, _ := helper.HashPassword("Password123!")
		testClient.User.Create().
			SetEmail("existing@example.com").
			SetUsername("existinguser").
			SetFullName("Existing User").
			SetPasswordHash(hashedPassword).
			Save(ctx)

		reqBody := model.SendOTPRequest{
			Email:        "existing@example.com",
			Mode:         "change_email",
			CaptchaToken: dummyTurnstileToken,
		}
		rr := makeRequest("POST", "/api/otp/send", reqBody, "")
		assert.Equal(t, http.StatusOK, rr.Code)

		key := fmt.Sprintf("otp:%s:%s", reqBody.Mode, reqBody.Email)
		_, err := redisAdapter.Get(ctx, key)
		assert.Error(t, err)
	})

	t.Run("Success - Rate Limit Recovery", func(t *testing.T) {
		clearDatabase(ctx)
		originalSecret := testConfig.TurnstileSecretKey
		testConfig.TurnstileSecretKey = cfTurnstileAlwaysPasses
		defer func() { testConfig.TurnstileSecretKey = originalSecret }()

		reqBody := model.SendOTPRequest{
			Email:        "recovery@example.com",
			Mode:         "register",
			CaptchaToken: dummyTurnstileToken,
		}

		for i := 0; i < 5; i++ {
			makeRequest("POST", "/api/otp/send", reqBody, "")
		}

		rrBlocked := makeRequest("POST", "/api/otp/send", reqBody, "")
		assert.Equal(t, http.StatusTooManyRequests, rrBlocked.Code)

		time.Sleep(3 * time.Second)

		rrRecovered := makeRequest("POST", "/api/otp/send", reqBody, "")
		assert.Equal(t, http.StatusOK, rrRecovered.Code)
	})
}
