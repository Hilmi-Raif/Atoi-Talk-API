package config

import "testing"

func TestNewS3ClientConfiguresStaticCredentialsAndEndpoint(t *testing.T) {
	client := NewS3Client(&AppConfig{
		S3Region:    "us-east-1",
		S3AccessKey: "access",
		S3SecretKey: "secret",
		S3Endpoint:  "http://localhost:9090",
	})
	if client == nil {
		t.Fatal("expected S3 client")
	}
	options := client.Options()
	if options.BaseEndpoint == nil || *options.BaseEndpoint != "http://localhost:9090" || !options.UsePathStyle {
		t.Fatalf("unexpected S3 endpoint options: %+v", options)
	}
	if options.Region != "us-east-1" {
		t.Fatalf("unexpected S3 region: %q", options.Region)
	}
}

func TestNewS3ClientWithoutEndpoint(t *testing.T) {
	client := NewS3Client(&AppConfig{S3Region: "us-east-1"})
	if client == nil {
		t.Fatal("expected S3 client without custom endpoint")
	}
	options := client.Options()
	if options.BaseEndpoint != nil || options.UsePathStyle {
		t.Fatalf("expected default S3 endpoint options, got %+v", options)
	}
}
