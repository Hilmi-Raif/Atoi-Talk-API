package controller

import (
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/model"
	"AtoiTalkAPI/internal/service"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
)

type AuthController struct {
	authService service.AuthServicePort
}

func NewAuthController(authService service.AuthServicePort) *AuthController {
	return &AuthController{
		authService: authService,
	}
}

// Login godoc
// @Summary      Login
// @Description  Login with email and password
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        request body model.LoginRequest true "Login Request"
// @Success      200  {object}  helper.ResponseSuccess{data=model.AuthResponse}
// @Failure      400  {object}  helper.ResponseError
// @Failure      401  {object}  helper.ResponseError
// @Failure      429  {object}  helper.ResponseError
// @Failure      500  {object}  helper.ResponseError
// @Router       /api/auth/login [post]
func (c *AuthController) Login(w http.ResponseWriter, r *http.Request) {
	var req model.LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Warn("Invalid request body", "error", err)
		helper.WriteError(w, helper.NewBadRequestError(""))
		return
	}

	resp, err := c.authService.Login(r.Context(), req)
	if err != nil {
		helper.WriteError(w, err)
		return
	}

	helper.WriteSuccess(w, resp)
}

// Logout godoc
// @Summary      Logout
// @Description  Invalidate the current access token and disconnect WebSocket.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Success      200  {object}  helper.ResponseSuccess
// @Failure      401  {object}  helper.ResponseError
// @Failure      429  {object}  helper.ResponseError
// @Failure      500  {object}  helper.ResponseError
// @Security     BearerAuth
// @Router       /api/auth/logout [post]
func (c *AuthController) Logout(w http.ResponseWriter, r *http.Request) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		helper.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}
	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || parts[0] != "Bearer" {
		helper.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}
	tokenString := parts[1]

	err := c.authService.Logout(r.Context(), tokenString)
	if err != nil {
		helper.WriteError(w, err)
		return
	}

	helper.WriteSuccess(w, nil)
}

// GoogleAuthInit godoc
// @Summary      Google OAuth Init
// @Description  Generate Google OAuth URL with one-time state and PKCE challenge.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Success      200  {object}  helper.ResponseSuccess{data=model.GoogleAuthInitResponse}
// @Failure      429  {object}  helper.ResponseError
// @Failure      500  {object}  helper.ResponseError
// @Router       /api/auth/google/init [get]
func (c *AuthController) GoogleAuthInit(w http.ResponseWriter, r *http.Request) {
	resp, err := c.authService.BeginGoogleAuth(r.Context())
	if err != nil {
		helper.WriteError(w, err)
		return
	}

	helper.WriteSuccess(w, resp)
}

// GoogleExchange godoc
// @Summary      Google Exchange
// @Description  Exchange Google authorization code + state for App Token and User Info
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        request body model.GoogleLoginRequest true "Google Login Request"
// @Success      200  {object}  helper.ResponseSuccess{data=model.AuthResponse}
// @Failure      400  {object}  helper.ResponseError
// @Failure      429  {object}  helper.ResponseError
// @Failure      500  {object}  helper.ResponseError
// @Router       /api/auth/google [post]
func (c *AuthController) GoogleExchange(w http.ResponseWriter, r *http.Request) {
	var req model.GoogleLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Warn("Invalid request body", "error", err)
		helper.WriteError(w, helper.NewBadRequestError(""))
		return
	}

	resp, err := c.authService.GoogleExchange(r.Context(), req)
	if err != nil {
		helper.WriteError(w, err)
		return
	}

	helper.WriteSuccess(w, resp)
}

// Register godoc
// @Summary      Register User
// @Description  Register a new user with email, password, and OTP verification.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        request body model.RegisterUserRequest true "Register User Request"
// @Success      200  {object}  helper.ResponseSuccess{data=model.AuthResponse}
// @Failure      400  {object}  helper.ResponseError
// @Failure      409  {object}  helper.ResponseError
// @Failure      429  {object}  helper.ResponseError
// @Failure      500  {object}  helper.ResponseError
// @Router       /api/auth/register [post]
func (c *AuthController) Register(w http.ResponseWriter, r *http.Request) {
	var req model.RegisterUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Warn("Invalid request body", "error", err)
		helper.WriteError(w, helper.NewBadRequestError(""))
		return
	}

	resp, err := c.authService.Register(r.Context(), req)
	if err != nil {
		helper.WriteError(w, err)
		return
	}

	helper.WriteSuccess(w, resp)
}

// ResetPassword godoc
// @Summary      Reset Password
// @Description  Reset user password using OTP verification.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        request body model.ResetPasswordRequest true "Reset Password Request"
// @Success      200  {object}  helper.ResponseSuccess
// @Failure      400  {object}  helper.ResponseError
// @Failure      404  {object}  helper.ResponseError
// @Failure      429  {object}  helper.ResponseError
// @Failure      500  {object}  helper.ResponseError
// @Router       /api/auth/reset-password [post]
func (c *AuthController) ResetPassword(w http.ResponseWriter, r *http.Request) {
	var req model.ResetPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Warn("Invalid request body", "error", err)
		helper.WriteError(w, helper.NewBadRequestError(""))
		return
	}

	err := c.authService.ResetPassword(r.Context(), req)
	if err != nil {
		helper.WriteError(w, err)
		return
	}

	helper.WriteSuccess(w, nil)
}
