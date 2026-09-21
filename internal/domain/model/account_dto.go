package model

type ChangePasswordRequest struct {
	OldPassword     *string `json:"old_password"`
	NewPassword     string  `json:"new_password" validate:"required,min=8,max=72,password_complexity"`
	ConfirmPassword string  `json:"confirm_password" validate:"required,eqfield=NewPassword"`
}

type ChangeEmailRequest struct {
	Email string `json:"email" validate:"required,email"`
	Code  string `json:"code" validate:"required,len=6"`
}

type DeleteAccountRequest struct {
	Password *string `json:"password"`
}
