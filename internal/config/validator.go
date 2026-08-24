package config

import (
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/go-playground/validator/v10"
)

func NewValidator() *validator.Validate {
	v := validator.New()
	_ = v.RegisterValidation("password_complexity", validatePasswordComplexity)
	_ = v.RegisterValidation("imagevalid", validateImage)
	return v
}

func validatePasswordComplexity(fl validator.FieldLevel) bool {
	password := fl.Field().String()
	var (
		hasUpper  bool
		hasLower  bool
		hasNumber bool
		hasSymbol bool
	)
	for _, char := range password {
		switch {
		case unicode.IsUpper(char):
			hasUpper = true
		case unicode.IsLower(char):
			hasLower = true
		case unicode.IsNumber(char):
			hasNumber = true
		case unicode.IsPunct(char) || unicode.IsSymbol(char):
			hasSymbol = true
		}
	}
	return hasUpper && hasLower && hasNumber && hasSymbol
}

func validateImage(fl validator.FieldLevel) bool {

	allowedExtensions := []string{".png", ".jpg", ".jpeg", ".jpe", ".jfif", ".jif", ".jfi"}
	allowedContentTypes := []string{"image/png", "image/jpeg", "image/pjpeg", "image/apng"}
	var defaultMaxSize int64 = 2
	defaultMaxWidth, defaultMaxHeight := 800, 800

	params := fl.Param()
	maxWidth, maxHeight := defaultMaxWidth, defaultMaxHeight
	maxSize := defaultMaxSize

	if params != "" {
		parts := strings.Split(params, "_")
		if len(parts) >= 2 {
			if w, err := strconv.Atoi(parts[0]); err == nil {
				maxWidth = w
			}
			if h, err := strconv.Atoi(parts[1]); err == nil {
				maxHeight = h
			}
			if len(parts) == 3 {
				if s, err := strconv.ParseInt(parts[2], 10, 64); err == nil {
					maxSize = s
				}
			}
		}
	}

	var file *multipart.FileHeader
	fieldInterface := fl.Field().Interface()

	if f, ok := fieldInterface.(*multipart.FileHeader); ok {
		file = f
	} else if f, ok := fieldInterface.(multipart.FileHeader); ok {
		file = &f
	}

	if file == nil {
		return true
	}

	if file.Size > maxSize*1024*1024 {
		slog.Info("Image validation failed: file too large", "size", file.Size, "max", maxSize*1024*1024)
		return false
	}

	extension := strings.ToLower(filepath.Ext(file.Filename))
	isValidExt := false
	for _, allowedExtension := range allowedExtensions {
		if extension == allowedExtension {
			isValidExt = true
			break
		}
	}
	if !isValidExt {
		slog.Info("Image validation failed: invalid extension", "ext", extension)
		return false
	}

	fileOpened, err := file.Open()
	if err != nil {
		slog.Error("Failed to open file for image validation", "err", err)
		return false
	}
	defer func() { _ = fileOpened.Close() }()

	fileHeader := make([]byte, 512)
	n, err := fileOpened.Read(fileHeader)
	if err != nil && err != io.EOF {
		slog.Error("Failed to read file header", "err", err)
		return false
	}
	if n == 0 {
		slog.Info("Image validation failed: empty file")
		return false
	}

	contentType := http.DetectContentType(fileHeader[:n])
	isValidType := false
	for _, allowedContentType := range allowedContentTypes {
		if contentType == allowedContentType {
			isValidType = true
			break
		}
	}
	if !isValidType {
		slog.Info("Image validation failed: invalid content type", "type", contentType)
		return false
	}

	if _, err := fileOpened.Seek(0, 0); err != nil {
		slog.Error("Failed to seek file", "err", err)
		return false
	}

	img, _, err := image.DecodeConfig(fileOpened)
	if err != nil {
		slog.Info("Image validation failed: could not decode image config", "err", err)
		return false
	}

	if img.Width > maxWidth || img.Height > maxHeight {
		slog.Info("Image validation failed: dimensions too large",
			"width", img.Width, "height", img.Height,
			"maxW", maxWidth, "maxH", maxHeight)
		return false
	}

	return true
}
