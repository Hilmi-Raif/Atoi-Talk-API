package helper

import (
	"os"
	"strings"
	"testing"
)

func TestNormalizeStoragePath(t *testing.T) {
	if res := NormalizeStoragePath("folder\\sub\\avatar.png"); res != "folder/sub/avatar.png" {
		t.Fatalf("expected forward slashes, got: %q", res)
	}
	if res := NormalizeStoragePath("folder//sub/avatar.png"); res != "folder/sub/avatar.png" {
		t.Fatalf("expected cleaned path, got: %q", res)
	}
}

func TestGenerateUniqueFileName(t *testing.T) {
	name := GenerateUniqueFileName("photo.PNG")
	if !strings.HasSuffix(name, ".png") {
		t.Fatalf("expected normalized extension: %q", name)
	}
	if name == GenerateUniqueFileName("photo.PNG") {
		t.Fatal("generated names must be unique")
	}
	if !strings.HasSuffix(GenerateUniqueFileName("photo"), ".jpg") {
		t.Fatal("missing extension should default to jpg")
	}
}

func TestDetectFileContentType(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "content-*.png")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Write([]byte("\x89PNG\r\n\x1a\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	contentType, err := DetectFileContentType(file)
	if err != nil || contentType != "image/png" {
		t.Fatalf("unexpected content type: %q err=%v", contentType, err)
	}
}

func TestDetectFileContentTypeRejectsEmptyAndReadErrors(t *testing.T) {
	empty, err := os.CreateTemp(t.TempDir(), "empty-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = empty.Close() }()
	if _, err := DetectFileContentType(empty); err == nil {
		t.Fatal("expected empty file error")
	}

	if _, err := DetectFileContentType(&failingMultipartFile{}); err == nil {
		t.Fatal("expected read error")
	}
}

type failingMultipartFile struct{}

func (*failingMultipartFile) Read([]byte) (int, error)          { return 0, os.ErrPermission }
func (*failingMultipartFile) ReadAt([]byte, int64) (int, error) { return 0, os.ErrPermission }
func (*failingMultipartFile) Seek(int64, int) (int64, error)    { return 0, nil }
func (*failingMultipartFile) Close() error                      { return nil }
