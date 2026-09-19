package helper

import (
	"encoding/base64"
	"fmt"
	"strings"
)

func DecodeCursor(cursor string, delimiter string) (string, string, error) {
	decodedBytes, err := base64.URLEncoding.DecodeString(cursor)
	if err != nil {
		return "", "", fmt.Errorf("invalid cursor encoding")
	}

	decodedString := string(decodedBytes)
	parts := strings.Split(decodedString, delimiter)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid cursor format")
	}

	return parts[0], parts[1], nil
}

func EncodeCursor(strPart string, idPart string, delimiter string) string {
	cursorString := fmt.Sprintf("%s%s%s", strPart, delimiter, idPart)
	return base64.URLEncoding.EncodeToString([]byte(cursorString))
}
