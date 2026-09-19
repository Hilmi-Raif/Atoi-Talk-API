package constant

type OTPMode string

const (
	ModeRegister    OTPMode = "register"
	ModeReset       OTPMode = "reset"
	ModeChangeEmail OTPMode = "change_email"
)
