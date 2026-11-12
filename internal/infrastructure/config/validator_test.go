package config

import (
	"AtoiTalkAPI/internal/domain/model"
	"bytes"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/go-playground/validator/v10"
)

func TestValidatorPasswordComplexity(t *testing.T) {
	validator := NewValidator()

	tests := []struct {
		name     string
		password string
		valid    bool
	}{
		{"valid", "Password123!", true},
		{"missing uppercase", "password123!", false},
		{"missing lowercase", "PASSWORD123!", false},
		{"missing number", "Password!", false},
		{"missing symbol", "Password123", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			type request struct {
				Password string `validate:"password_complexity"`
			}
			err := validator.Struct(request{Password: tt.password})
			if (err == nil) != tt.valid {
				t.Fatalf("expected valid=%v, got error=%v", tt.valid, err)
			}
		})
	}
}

func TestValidatorImageAcceptsValidPNG(t *testing.T) {
	file := multipartFileHeader(t, "avatar.png", pngBytes(t, 10, 10))
	validator := NewValidator()

	type request struct {
		Avatar *multipart.FileHeader `validate:"imagevalid"`
	}
	if err := validator.Struct(request{Avatar: file}); err != nil {
		t.Fatalf("expected valid image, got %v", err)
	}
}

func TestValidatorImageRejectsInvalidFiles(t *testing.T) {
	validator := NewValidator()
	type request struct {
		Avatar *multipart.FileHeader `validate:"imagevalid"`
	}

	tests := []struct {
		name string
		file *multipart.FileHeader
	}{
		{"invalid extension", multipartFileHeader(t, "avatar.gif", pngBytes(t, 10, 10))},
		{"invalid content", multipartFileHeader(t, "avatar.png", []byte("not an image"))},
		{"empty file", multipartFileHeader(t, "avatar.png", nil)},
		{"too large", func() *multipart.FileHeader {
			file := multipartFileHeader(t, "avatar.png", pngBytes(t, 10, 10))
			file.Size = 3 * 1024 * 1024
			return file
		}()},
		{"too wide", multipartFileHeader(t, "avatar.png", pngBytes(t, 801, 1))},
		{"too tall", multipartFileHeader(t, "avatar.png", pngBytes(t, 1, 801))},
		{"corrupted image", multipartFileHeader(t, "avatar.png", append(pngBytes(t, 10, 10)[:16], []byte("corrupt")...))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validator.Struct(request{Avatar: tt.file}); err == nil {
				t.Fatal("expected image validation error")
			}
		})
	}
}

func TestValidatorImageSupportsCustomLimitsAndValueHeaders(t *testing.T) {
	validator := NewValidator()
	file := *multipartFileHeader(t, "avatar.jpg", pngBytes(t, 10, 10))
	type request struct {
		Avatar multipart.FileHeader `validate:"imagevalid=20_20_1"`
	}
	if err := validator.Struct(request{Avatar: file}); err != nil {
		t.Fatalf("expected value FileHeader with custom limits to pass: %v", err)
	}

	file.Size = 2 * 1024 * 1024
	if err := validator.Struct(request{Avatar: file}); err == nil {
		t.Fatal("expected custom size limit to reject file")
	}

	tallFile := *multipartFileHeader(t, "avatar.jpg", pngBytes(t, 10, 30))
	type tallRequest struct {
		Avatar multipart.FileHeader `validate:"imagevalid=20_20"`
	}
	if err := validator.Struct(tallRequest{Avatar: tallFile}); err == nil {
		t.Fatal("expected custom height limit to reject tall image")
	}
}

func TestValidatorImagePassesForNonFileField(t *testing.T) {
	validator := NewValidator()
	type request struct {
		Avatar string `validate:"imagevalid"`
	}
	if err := validator.Struct(request{Avatar: "not-a-file"}); err != nil {
		t.Fatalf("expected non-file field to pass validator: %v", err)
	}
}

func TestValidatorCaptchaModes(t *testing.T) {
	requests := map[string]interface{}{
		"login":    model.LoginRequest{Email: "test@example.com", Password: "StrongPassword1!"},
		"otp":      model.SendOTPRequest{Email: "test@example.com", Mode: "register"},
		"register": model.RegisterUserRequest{Email: "test@example.com", Username: "tester", Code: "123456", FullName: "Test User", Password: "StrongPassword1!"},
		"reset":    model.ResetPasswordRequest{Email: "test@example.com", Code: "123456", Password: "StrongPassword1!", ConfirmPassword: "StrongPassword1!"},
		"upload":   model.UploadMediaRequest{Usage: "message_attachment", OriginalName: "notes.txt", FileSize: 10, MimeType: "text/plain"},
		"avatar":   model.UploadMediaRequest{Usage: "user_avatar", OriginalName: "avatar.png", FileSize: 10, MimeType: "image/png"},
		"group":    model.UploadMediaRequest{Usage: "group_avatar", OriginalName: "avatar.png", FileSize: 10, MimeType: "image/png"},
	}
	for name, request := range requests {
		t.Run(name, func(t *testing.T) {
			if err := NewValidator(false).Struct(request); err != nil {
				t.Fatalf("disabled captcha rejected request without token: %v", err)
			}
			if err := NewValidator(true).Struct(request); err == nil {
				t.Fatal("enabled captcha accepted missing token")
			}
			if err := NewValidator().Struct(request); err == nil {
				t.Fatal("default captcha accepted missing token")
			}
			value := reflect.New(reflect.TypeOf(request))
			value.Elem().Set(reflect.ValueOf(request))
			value.Elem().FieldByName("CaptchaToken").SetString(" \t\n")
			if err := NewValidator(true).Struct(value.Interface()); err == nil {
				t.Fatal("enabled captcha accepted whitespace token")
			}
			value.Elem().FieldByName("CaptchaToken").SetString("provider-token")
			for _, enabled := range []bool{true, false} {
				if err := NewValidator(enabled).Struct(value.Interface()); err != nil {
					t.Fatalf("captcha enabled=%t rejected populated token: %v", enabled, err)
				}
			}
		})
	}
}

func TestValidatorDisabledCaptchaPreservesOtherValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		request interface{}
		field   string
	}{
		{"email", model.LoginRequest{Email: "invalid", Password: "StrongPassword1!"}, "Email"},
		{"password", model.LoginRequest{Email: "test@example.com", Password: "weak"}, "Password"},
		{"upload usage", model.UploadMediaRequest{Usage: "invalid", OriginalName: "notes.txt", FileSize: 10, MimeType: "text/plain"}, "Usage"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := NewValidator(false).Struct(tc.request)
			if err == nil {
				t.Fatalf("disabled captcha bypassed %s validation", tc.field)
			}
			found := false
			for _, field := range err.(validator.ValidationErrors) {
				if field.Field() == "CaptchaToken" {
					t.Fatal("disabled captcha still required a token")
				}
				found = found || field.Field() == tc.field
			}
			if !found {
				t.Fatalf("missing validation error for %s: %v", tc.field, err)
			}
		})
	}
}

func pngBytes(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func multipartFileHeader(t *testing.T, filename string, content []byte) *multipart.FileHeader {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("avatar", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if err := req.ParseMultipartForm(10 << 20); err != nil {
		t.Fatal(err)
	}
	file := req.MultipartForm.File["avatar"][0]
	t.Cleanup(func() { _ = req.MultipartForm.RemoveAll() })
	return file
}
