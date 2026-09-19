package model

type GoogleLoginRequest struct {
	Code  string `json:"code" validate:"required"`
	State string `json:"state" validate:"required,min=16,max=128"`
}

type GoogleAuthInitResponse struct {
	AuthURL          string `json:"auth_url"`
	State            string `json:"state"`
	ExpiresInSeconds int    `json:"expires_in_seconds"`
}

type LoginRequest struct {
	Email        string `json:"email" validate:"required,email"`
	Password     string `json:"password" validate:"required,min=8,max=72,password_complexity"`
	CaptchaToken string `json:"captcha_token" validate:"required"`
}

type AuthResponse struct {
	Token string  `json:"token"`
	User  UserDTO `json:"user"`
}

type SendOTPRequest struct {
	Email        string `json:"email" validate:"required,email"`
	Mode         string `json:"mode" validate:"required,oneof=register reset change_email"`
	CaptchaToken string `json:"captcha_token" validate:"required"`
}

type RegisterUserRequest struct {
	Email        string `json:"email" validate:"required,email"`
	Username     string `json:"username" validate:"required,min=3,max=50,alphanum"`
	Code         string `json:"code" validate:"required,len=6"`
	FullName     string `json:"full_name" validate:"required,min=3,max=100"`
	Password     string `json:"password" validate:"required,min=8,max=72,password_complexity"`
	CaptchaToken string `json:"captcha_token" validate:"required"`
}

type ResetPasswordRequest struct {
	Email           string `json:"email" validate:"required,email"`
	Code            string `json:"code" validate:"required,len=6"`
	Password        string `json:"password" validate:"required,min=8,max=72,password_complexity"`
	ConfirmPassword string `json:"confirm_password" validate:"required,eqfield=Password"`
	CaptchaToken    string `json:"captcha_token" validate:"required"`
}
