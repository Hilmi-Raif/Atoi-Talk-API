package helper

import (
	"bytes"
	"embed"
	"html/template"
	"strings"
)

func GenerateEmailBody(templateFS embed.FS, templateName string, data any) (string, error) {
	t, err := template.ParseFS(templateFS, templateName)
	if err != nil {
		return "", err
	}

	var body bytes.Buffer
	if err := t.Execute(&body, data); err != nil {
		return "", err
	}

	return body.String(), nil
}

func NormalizeEmail(email string) string {
	email = strings.TrimSpace(strings.ToLower(email))

	atIndex := strings.LastIndex(email, "@")
	if atIndex <= 0 {
		return email
	}

	localPart := email[:atIndex]
	domainPart := email[atIndex+1:]

	if plusIndex := strings.IndexByte(localPart, '+'); plusIndex != -1 {
		localPart = localPart[:plusIndex]
	}

	if domainPart == "gmail.com" || domainPart == "googlemail.com" {
		domainPart = "gmail.com"
		localPart = strings.ReplaceAll(localPart, ".", "")
	}

	return localPart + "@" + domainPart
}
