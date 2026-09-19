package captcha

type Verifier interface {
	Verify(string, string) error
}
