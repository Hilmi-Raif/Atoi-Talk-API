//go:build integration

package integration

import (
	"AtoiTalkAPI/ent/media"
	"AtoiTalkAPI/ent/user"
	"AtoiTalkAPI/ent/useridentity"
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/model"
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestLogin(t *testing.T) {
	validPassword := "Password123!"

	t.Run("Success", func(t *testing.T) {
		clearDatabase(context.Background())
		u := createTestUser(t, "loginsuccess")
		validEmail := *u.Email
		validUsername := *u.Username

		originalSecret := testConfig.TurnstileSecretKey
		testConfig.TurnstileSecretKey = cfTurnstileAlwaysPasses
		defer func() { testConfig.TurnstileSecretKey = originalSecret }()

		reqBody := model.LoginRequest{
			Email:        validEmail,
			Password:     validPassword,
			CaptchaToken: dummyTurnstileToken,
		}
		rr := makeRequest("POST", "/api/auth/login", reqBody, "")
		assert.Equal(t, http.StatusOK, rr.Code)

		resp := parseResponse[model.AuthResponse](t, rr)
		assert.NotEmpty(t, resp.Token)
		assert.Equal(t, validEmail, resp.User.Email)
		assert.Equal(t, validUsername, resp.User.Username)
	})

	t.Run("Invalid Captcha", func(t *testing.T) {
		clearDatabase(context.Background())
		u := createTestUser(t, "logincaptcha")
		validEmail := *u.Email

		originalSecret := testConfig.TurnstileSecretKey
		testConfig.TurnstileSecretKey = cfTurnstileAlwaysFails
		defer func() { testConfig.TurnstileSecretKey = originalSecret }()

		reqBody := model.LoginRequest{
			Email:        validEmail,
			Password:     validPassword,
			CaptchaToken: dummyTurnstileToken,
		}
		rr := makeRequest("POST", "/api/auth/login", reqBody, "")
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("User Not Found", func(t *testing.T) {
		clearDatabase(context.Background())

		originalSecret := testConfig.TurnstileSecretKey
		testConfig.TurnstileSecretKey = cfTurnstileAlwaysPasses
		defer func() { testConfig.TurnstileSecretKey = originalSecret }()

		reqBody := model.LoginRequest{
			Email:        "nonexistent@example.com",
			Password:     validPassword,
			CaptchaToken: dummyTurnstileToken,
		}
		rr := makeRequest("POST", "/api/auth/login", reqBody, "")
		assert.Equal(t, http.StatusUnauthorized, rr.Code)
	})

	t.Run("Invalid Password", func(t *testing.T) {
		clearDatabase(context.Background())
		u := createTestUser(t, "loginwrongpass")
		validEmail := *u.Email

		originalSecret := testConfig.TurnstileSecretKey
		testConfig.TurnstileSecretKey = cfTurnstileAlwaysPasses
		defer func() { testConfig.TurnstileSecretKey = originalSecret }()

		reqBody := model.LoginRequest{
			Email:        validEmail,
			Password:     "WrongPassword123!",
			CaptchaToken: dummyTurnstileToken,
		}
		rr := makeRequest("POST", "/api/auth/login", reqBody, "")
		assert.Equal(t, http.StatusUnauthorized, rr.Code)
	})

	t.Run("Fail - Login Deleted User", func(t *testing.T) {
		clearDatabase(context.Background())
		originalSecret := testConfig.TurnstileSecretKey
		testConfig.TurnstileSecretKey = cfTurnstileAlwaysPasses
		defer func() { testConfig.TurnstileSecretKey = originalSecret }()

		hashedPassword, _ := helper.HashPassword(validPassword)

		email := fmt.Sprintf("deletedlogin%d@test.com", time.Now().UnixNano())
		username := fmt.Sprintf("deletedlogin%d", time.Now().UnixNano())

		testClient.User.Create().
			SetEmail(email).
			SetUsername(username).
			SetFullName("Deleted User").
			SetPasswordHash(hashedPassword).
			SetDeletedAt(time.Now().UTC()).
			Save(context.Background())

		reqBody := model.LoginRequest{
			Email:        email,
			Password:     validPassword,
			CaptchaToken: dummyTurnstileToken,
		}
		rr := makeRequest("POST", "/api/auth/login", reqBody, "")
		assert.Equal(t, http.StatusUnauthorized, rr.Code)
	})
}

func TestLogout(t *testing.T) {
	clearDatabase(context.Background())
	u := createTestUser(t, "logoutuser")
	token, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u.ID)

	t.Run("Success Logout", func(t *testing.T) {
		rr := makeRequest("POST", "/api/auth/logout", nil, token)
		assert.Equal(t, http.StatusOK, rr.Code)

		key := fmt.Sprintf("blacklist:%s", token)
		val, err := redisAdapter.Get(context.Background(), key)
		assert.NoError(t, err)
		assert.Equal(t, "revoked", val)

		rr2 := makeRequest("GET", "/api/user/current", nil, token)
		assert.Equal(t, http.StatusUnauthorized, rr2.Code)
	})
}

func TestGoogleExchange(t *testing.T) {
	clearDatabase(context.Background())

	t.Run("Validation Error", func(t *testing.T) {
		reqBody := model.GoogleLoginRequest{Code: ""}
		rr := makeRequest("POST", "/api/auth/google", reqBody, "")
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Invalid Code", func(t *testing.T) {
		reqBody := model.GoogleLoginRequest{
			Code:  "invalid-auth-code",
			State: "invalidstate123456",
		}
		rr := makeRequest("POST", "/api/auth/google", reqBody, "")
		assert.Equal(t, http.StatusUnauthorized, rr.Code)
	})

	t.Run("Valid Code", func(t *testing.T) {
		validCode := os.Getenv("TEST_GOOGLE_AUTH_CODE")
		validState := os.Getenv("TEST_GOOGLE_AUTH_STATE")
		if validCode == "" || validState == "" {
			t.Skip("Skipping Valid Code test: TEST_GOOGLE_AUTH_CODE or TEST_GOOGLE_AUTH_STATE not set")
		}

		t.Run("Register and Link Identity", func(t *testing.T) {
			clearDatabase(context.Background())
			reqBody := model.GoogleLoginRequest{
				Code:  validCode,
				State: validState,
			}
			rr := makeRequest("POST", "/api/auth/google", reqBody, "")
			assert.Equal(t, http.StatusOK, rr.Code)

			resp := parseResponse[model.AuthResponse](t, rr)
			assert.NotEmpty(t, resp.Token)
			assert.NotEmpty(t, resp.User.Email)
			assert.NotEmpty(t, resp.User.Username)

			userID := resp.User.ID
			identity, err := testClient.UserIdentity.Query().
				Where(useridentity.UserID(userID)).
				Only(context.Background())
			assert.NoError(t, err)
			assert.Equal(t, useridentity.ProviderGoogle, identity.Provider)
			assert.NotEmpty(t, identity.ProviderID)

			if resp.User.Avatar != "" {
				parts := strings.Split(resp.User.Avatar, "/")
				fileName := parts[len(parts)-1]

				m, err := testClient.Media.Query().Where(media.FileName(fileName)).Only(context.Background())
				assert.NoError(t, err)
				assert.Equal(t, userID, m.UploadedByID)
			}
		})
	})
}

func TestGoogleAuthInit(t *testing.T) {
	clearDatabase(context.Background())

	rr := makeRequest("GET", "/api/auth/google/init", nil, "")
	assert.Equal(t, http.StatusOK, rr.Code)

	resp := parseResponse[model.GoogleAuthInitResponse](t, rr)
	assert.NotEmpty(t, resp.AuthURL)
	assert.NotEmpty(t, resp.State)
	assert.Greater(t, resp.ExpiresInSeconds, 0)
	assert.Contains(t, resp.AuthURL, "state="+resp.State)
}

func TestRegister(t *testing.T) {
	validCode := "123456"

	t.Run("Success", func(t *testing.T) {
		clearDatabase(context.Background())

		validEmail := fmt.Sprintf("regsuccess%d@example.com", time.Now().UnixNano())
		validUsername := fmt.Sprintf("reguser%d", time.Now().UnixNano())

		originalSecret := testConfig.TurnstileSecretKey
		testConfig.TurnstileSecretKey = cfTurnstileAlwaysPasses
		defer func() { testConfig.TurnstileSecretKey = originalSecret }()

		createOTP(validEmail, validCode, time.Now().UTC().Add(5*time.Minute))

		reqBody := model.RegisterUserRequest{
			Email:        validEmail,
			Username:     validUsername,
			Code:         validCode,
			FullName:     "Test User",
			Password:     "Password123!",
			CaptchaToken: dummyTurnstileToken,
		}
		rr := makeRequest("POST", "/api/auth/register", reqBody, "")
		assert.Equal(t, http.StatusOK, rr.Code)

		resp := parseResponse[model.AuthResponse](t, rr)
		assert.NotEmpty(t, resp.Token)
		assert.Equal(t, validEmail, resp.User.Email)
		assert.Equal(t, validUsername, resp.User.Username)
	})

	t.Run("Success - Register with Whitespace", func(t *testing.T) {
		clearDatabase(context.Background())

		validEmail := fmt.Sprintf("  regspace%d@example.com  ", time.Now().UnixNano())
		validUsername := fmt.Sprintf("  regspace%d  ", time.Now().UnixNano())
		cleanEmail := strings.TrimSpace(validEmail)
		cleanUsername := strings.TrimSpace(validUsername)

		originalSecret := testConfig.TurnstileSecretKey
		testConfig.TurnstileSecretKey = cfTurnstileAlwaysPasses
		defer func() { testConfig.TurnstileSecretKey = originalSecret }()

		createOTP(cleanEmail, validCode, time.Now().UTC().Add(5*time.Minute))

		reqBody := model.RegisterUserRequest{
			Email:        validEmail,
			Username:     validUsername,
			Code:         validCode,
			FullName:     "  Test User  ",
			Password:     "Password123!",
			CaptchaToken: dummyTurnstileToken,
		}
		rr := makeRequest("POST", "/api/auth/register", reqBody, "")
		assert.Equal(t, http.StatusOK, rr.Code)

		resp := parseResponse[model.AuthResponse](t, rr)
		assert.NotEmpty(t, resp.Token)
		assert.Equal(t, cleanEmail, resp.User.Email)
		assert.Equal(t, cleanUsername, resp.User.Username)
		assert.Equal(t, "Test User", resp.User.FullName)
	})

	t.Run("Username Already Taken", func(t *testing.T) {
		clearDatabase(context.Background())
		u := createTestUser(t, "regtaken")

		validEmail := fmt.Sprintf("regnew%d@example.com", time.Now().UnixNano())

		originalSecret := testConfig.TurnstileSecretKey
		testConfig.TurnstileSecretKey = cfTurnstileAlwaysPasses
		defer func() { testConfig.TurnstileSecretKey = originalSecret }()

		createOTP(validEmail, validCode, time.Now().UTC().Add(5*time.Minute))

		reqBody := model.RegisterUserRequest{
			Email:        validEmail,
			Username:     *u.Username,
			Code:         validCode,
			FullName:     "Test User",
			Password:     "Password123!",
			CaptchaToken: dummyTurnstileToken,
		}
		rr := makeRequest("POST", "/api/auth/register", reqBody, "")
		assert.Equal(t, http.StatusConflict, rr.Code)
	})

	t.Run("Invalid Captcha", func(t *testing.T) {
		clearDatabase(context.Background())
		validEmail := fmt.Sprintf("regcaptcha%d@example.com", time.Now().UnixNano())
		validUsername := fmt.Sprintf("regcaptcha%d", time.Now().UnixNano())

		originalSecret := testConfig.TurnstileSecretKey
		testConfig.TurnstileSecretKey = cfTurnstileAlwaysFails
		defer func() { testConfig.TurnstileSecretKey = originalSecret }()

		createOTP(validEmail, validCode, time.Now().UTC().Add(5*time.Minute))

		reqBody := model.RegisterUserRequest{
			Email:        validEmail,
			Username:     validUsername,
			Code:         validCode,
			FullName:     "Test User",
			Password:     "Password123!",
			CaptchaToken: dummyTurnstileToken,
		}
		rr := makeRequest("POST", "/api/auth/register", reqBody, "")
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Invalid OTP", func(t *testing.T) {
		clearDatabase(context.Background())
		validEmail := fmt.Sprintf("regotp%d@example.com", time.Now().UnixNano())
		validUsername := fmt.Sprintf("regotp%d", time.Now().UnixNano())

		originalSecret := testConfig.TurnstileSecretKey
		testConfig.TurnstileSecretKey = cfTurnstileAlwaysPasses
		defer func() { testConfig.TurnstileSecretKey = originalSecret }()

		createOTP(validEmail, validCode, time.Now().UTC().Add(5*time.Minute))

		reqBody := model.RegisterUserRequest{
			Email:        validEmail,
			Username:     validUsername,
			Code:         "000000",
			FullName:     "Test User",
			Password:     "Password123!",
			CaptchaToken: dummyTurnstileToken,
		}
		rr := makeRequest("POST", "/api/auth/register", reqBody, "")
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Expired OTP", func(t *testing.T) {
		clearDatabase(context.Background())
		validEmail := fmt.Sprintf("regexpired%d@example.com", time.Now().UnixNano())
		validUsername := fmt.Sprintf("regexpired%d", time.Now().UnixNano())

		originalSecret := testConfig.TurnstileSecretKey
		testConfig.TurnstileSecretKey = cfTurnstileAlwaysPasses
		defer func() { testConfig.TurnstileSecretKey = originalSecret }()

		createOTP(validEmail, validCode, time.Now().UTC().Add(-5*time.Minute))

		reqBody := model.RegisterUserRequest{
			Email:        validEmail,
			Username:     validUsername,
			Code:         validCode,
			FullName:     "Test User",
			Password:     "Password123!",
			CaptchaToken: dummyTurnstileToken,
		}
		rr := makeRequest("POST", "/api/auth/register", reqBody, "")
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Email Already Registered", func(t *testing.T) {
		clearDatabase(context.Background())
		u := createTestUser(t, "regexist")
		validEmail := *u.Email
		validUsername := fmt.Sprintf("regnew%d", time.Now().UnixNano())

		originalSecret := testConfig.TurnstileSecretKey
		testConfig.TurnstileSecretKey = cfTurnstileAlwaysPasses
		defer func() { testConfig.TurnstileSecretKey = originalSecret }()

		createOTP(validEmail, validCode, time.Now().UTC().Add(5*time.Minute))

		reqBody := model.RegisterUserRequest{
			Email:        validEmail,
			Username:     validUsername,
			Code:         validCode,
			FullName:     "Test User",
			Password:     "Password123!",
			CaptchaToken: dummyTurnstileToken,
		}
		rr := makeRequest("POST", "/api/auth/register", reqBody, "")
		assert.Equal(t, http.StatusConflict, rr.Code)
	})
}

func TestResetPassword(t *testing.T) {
	validCode := "123456"
	newPassword := "NewPassword123!"

	t.Run("Success", func(t *testing.T) {
		clearDatabase(context.Background())
		u := createTestUser(t, "resetsuccess")
		validEmail := *u.Email

		originalSecret := testConfig.TurnstileSecretKey
		testConfig.TurnstileSecretKey = cfTurnstileAlwaysPasses
		defer func() { testConfig.TurnstileSecretKey = originalSecret }()

		createOTP(validEmail, validCode, time.Now().UTC().Add(5*time.Minute))

		key := fmt.Sprintf("otp:%s:%s", "reset", validEmail)
		hashedCode := helper.HashOTP(validCode, testConfig.OTPSecret)
		redisAdapter.Set(context.Background(), key, hashedCode, 5*time.Minute)

		reqBody := model.ResetPasswordRequest{
			Email:           validEmail,
			Code:            validCode,
			Password:        newPassword,
			ConfirmPassword: newPassword,
			CaptchaToken:    dummyTurnstileToken,
		}
		rr := makeRequest("POST", "/api/auth/reset-password", reqBody, "")
		assert.Equal(t, http.StatusOK, rr.Code)

		u, _ = testClient.User.Query().Where(user.Email(validEmail)).Only(context.Background())
		assert.NotNil(t, u.PasswordHash)
		assert.True(t, helper.CheckPasswordHash(newPassword, *u.PasswordHash))
	})

	t.Run("User Not Found", func(t *testing.T) {
		clearDatabase(context.Background())
		validEmail := fmt.Sprintf("reset404%d@example.com", time.Now().UnixNano())

		originalSecret := testConfig.TurnstileSecretKey
		testConfig.TurnstileSecretKey = cfTurnstileAlwaysPasses
		defer func() { testConfig.TurnstileSecretKey = originalSecret }()

		createOTP(validEmail, validCode, time.Now().UTC().Add(5*time.Minute))
		key := fmt.Sprintf("otp:%s:%s", "reset", validEmail)
		hashedCode := helper.HashOTP(validCode, testConfig.OTPSecret)
		redisAdapter.Set(context.Background(), key, hashedCode, 5*time.Minute)

		reqBody := model.ResetPasswordRequest{
			Email:           validEmail,
			Code:            validCode,
			Password:        newPassword,
			ConfirmPassword: newPassword,
			CaptchaToken:    dummyTurnstileToken,
		}
		rr := makeRequest("POST", "/api/auth/reset-password", reqBody, "")
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})

	t.Run("Invalid OTP", func(t *testing.T) {
		clearDatabase(context.Background())
		u := createTestUser(t, "resetotp")
		validEmail := *u.Email

		originalSecret := testConfig.TurnstileSecretKey
		testConfig.TurnstileSecretKey = cfTurnstileAlwaysPasses
		defer func() { testConfig.TurnstileSecretKey = originalSecret }()

		createOTP(validEmail, validCode, time.Now().UTC().Add(5*time.Minute))
		key := fmt.Sprintf("otp:%s:%s", "reset", validEmail)
		hashedCode := helper.HashOTP(validCode, testConfig.OTPSecret)
		redisAdapter.Set(context.Background(), key, hashedCode, 5*time.Minute)

		reqBody := model.ResetPasswordRequest{
			Email:           validEmail,
			Code:            "000000",
			Password:        newPassword,
			ConfirmPassword: newPassword,
			CaptchaToken:    dummyTurnstileToken,
		}
		rr := makeRequest("POST", "/api/auth/reset-password", reqBody, "")
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Password Mismatch", func(t *testing.T) {
		clearDatabase(context.Background())
		validEmail := fmt.Sprintf("resetmismatch%d@example.com", time.Now().UnixNano())

		reqBody := model.ResetPasswordRequest{
			Email:           validEmail,
			Code:            validCode,
			Password:        newPassword,
			ConfirmPassword: "DifferentPassword123!",
			CaptchaToken:    dummyTurnstileToken,
		}
		rr := makeRequest("POST", "/api/auth/reset-password", reqBody, "")
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Token Integrity", func(t *testing.T) {
		clearDatabase(context.Background())
		u := createTestUser(t, "tokenintegrity")

		t.Run("Expired Token", func(t *testing.T) {
			token, _ := helper.GenerateJWT(testConfig.JWTSecret, -1, u.ID)
			rr := makeRequest("GET", "/api/user/current", nil, token)
			assert.Equal(t, http.StatusUnauthorized, rr.Code)
		})

		t.Run("Invalid Signature", func(t *testing.T) {
			token, _ := helper.GenerateJWT("wrong-secret", 3600, u.ID)
			rr := makeRequest("GET", "/api/user/current", nil, token)
			assert.Equal(t, http.StatusUnauthorized, rr.Code)
		})

		t.Run("Deleted User", func(t *testing.T) {
			token, _ := helper.GenerateJWT(testConfig.JWTSecret, 3600, u.ID)
			testClient.User.DeleteOneID(u.ID).Exec(context.Background())
			rr := makeRequest("GET", "/api/user/current", nil, token)
			assert.Equal(t, http.StatusUnauthorized, rr.Code)
		})
	})
}
