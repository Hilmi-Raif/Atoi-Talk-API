package config

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
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
