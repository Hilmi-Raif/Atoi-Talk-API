package model

import (
	"time"

	"github.com/google/uuid"
)

type UploadMediaRequest struct {
	Usage        string `json:"usage" validate:"required,oneof=message_attachment user_avatar group_avatar"`
	OriginalName string `json:"original_name" validate:"required,max=255"`
	FileSize     int64  `json:"file_size" validate:"required,gt=0"`
	MimeType     string `json:"mime_type" validate:"required,max=100"`
	CaptchaToken string `json:"captcha_token" validate:"required"`
}

type MediaDTO struct {
	ID           uuid.UUID `json:"id"`
	FileName     string    `json:"file_name"`
	OriginalName string    `json:"original_name"`
	FileSize     int64     `json:"file_size"`
	MimeType     string    `json:"mime_type"`
	Category     string    `json:"category"`
	UploadStatus string    `json:"upload_status"`
	URL          string    `json:"url"`
}

type UploadMediaResponse struct {
	Media         MediaDTO          `json:"media"`
	UploadURL     string            `json:"upload_url"`
	UploadMethod  string            `json:"upload_method"`
	UploadHeaders map[string]string `json:"upload_headers"`
	ExpiresAt     time.Time         `json:"expires_at"`
}

type MediaURLResponse struct {
	URL string `json:"url"`
}
