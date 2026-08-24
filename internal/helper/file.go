package helper

import (
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

func NormalizeStoragePath(p string) string {
	return path.Clean(strings.ReplaceAll(p, "\\", "/"))
}

func GenerateUniqueFileName(originalName string) string {
	ext := filepath.Ext(originalName)
	if ext == "" {
		ext = ".jpg"
	}

	ext = strings.ToLower(ext)

	uniqueName := fmt.Sprintf("%d-%s%s", time.Now().UTC().UnixNano(), uuid.New().String(), ext)

	return uniqueName
}

func DetectFileContentType(file multipart.File) (string, error) {
	buffer := make([]byte, 512)
	n, err := file.Read(buffer)
	if err != nil && err != io.EOF {
		return "", err
	}
	if n == 0 {
		return "", errors.New("empty file")
	}

	contentType := http.DetectContentType(buffer[:n])

	if _, err := file.Seek(0, 0); err != nil {
		return "", err
	}

	return contentType, nil
}
