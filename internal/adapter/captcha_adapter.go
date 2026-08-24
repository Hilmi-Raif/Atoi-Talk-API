package adapter

import (
	"AtoiTalkAPI/internal/config"
	"AtoiTalkAPI/internal/helper"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const maxCaptchaResponseBytes = 64 * 1024

type CaptchaAdapter struct {
	httpClient *http.Client
	cfg        *config.AppConfig
}

func NewCaptchaAdapter(cfg *config.AppConfig, httpClient *http.Client) *CaptchaAdapter {
	return &CaptchaAdapter{
		httpClient: httpClient,
		cfg:        cfg,
	}
}

type turnstileVerifyResponse struct {
	Success    bool     `json:"success"`
	ErrorCodes []string `json:"error-codes"`
}

func (c *CaptchaAdapter) Verify(token string, ip string) error {
	url := "https://challenges.cloudflare.com/turnstile/v0/siteverify"

	payload := map[string]string{
		"secret":   c.cfg.TurnstileSecretKey,
		"response": token,
		"remoteip": ip,
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal captcha payload: %w", err)
	}

	operation := func() (*http.Response, bool, error) {
		resp, err := c.httpClient.Post(url, "application/json", bytes.NewBuffer(jsonPayload))
		if helper.ShouldRetryHTTP(resp, err) {
			if resp != nil {
				_ = resp.Body.Close()
			}
			if err == nil && resp != nil {
				err = fmt.Errorf("captcha verification failed with status: %d", resp.StatusCode)
			}
			return nil, true, err
		}
		if err != nil {
			return nil, false, err
		}

		if resp.StatusCode != http.StatusOK {
			defer func() { _ = resp.Body.Close() }()
			return nil, false, fmt.Errorf("captcha verification failed with status: %d", resp.StatusCode)
		}

		return resp, false, nil
	}

	resp, err := helper.RetryWithBackoff(operation, 3, 500*time.Millisecond)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCaptchaResponseBytes+1))
	if err != nil {
		return fmt.Errorf("failed to read captcha response body: %w", err)
	}
	if len(body) > maxCaptchaResponseBytes {
		return fmt.Errorf("captcha response body exceeds %d bytes", maxCaptchaResponseBytes)
	}

	var verifyResp turnstileVerifyResponse
	if err := json.Unmarshal(body, &verifyResp); err != nil {
		return fmt.Errorf("failed to unmarshal captcha response: %w", err)
	}

	if !verifyResp.Success {
		return fmt.Errorf("captcha verification failed: %v", verifyResp.ErrorCodes)
	}

	return nil
}
