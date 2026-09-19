package helper

import "testing"

func TestHashAndRandomHelpers(t *testing.T) {
	hashed, err := HashPassword("Password123!")
	if err != nil || !CheckPasswordHash("Password123!", hashed) || CheckPasswordHash("wrong", hashed) {
		t.Fatalf("password hashing contract failed: err=%v", err)
	}
	if HashOTP("123456", "secret") == HashOTP("123456", "other") {
		t.Fatal("OTP hash must depend on secret")
	}
	random, err := GenerateRandomString(32)
	if err != nil || len(random) != 32 {
		t.Fatalf("unexpected random string: len=%d err=%v", len(random), err)
	}
}
