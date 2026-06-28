package adapter

import (
	"AtoiTalkAPI/internal/config"
	"AtoiTalkAPI/internal/helper"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type StorageAdapter struct {
	client        *s3.Client
	bucketPublic  string
	bucketPrivate string
	region        string
	endpoint      string
	publicDomain  string
	httpClient    *http.Client
	presignClient *s3.PresignClient
}

type StorageObjectInfo struct {
	Size        int64
	ContentType string
}

func NewStorageAdapter(cfg *config.AppConfig, s3Client *s3.Client, httpClient *http.Client) *StorageAdapter {
	var presignClient *s3.PresignClient
	if s3Client != nil {
		presignClient = s3.NewPresignClient(s3Client)
	}

	return &StorageAdapter{
		client:        s3Client,
		bucketPublic:  cfg.S3BucketPublic,
		bucketPrivate: cfg.S3BucketPrivate,
		region:        cfg.S3Region,
		endpoint:      cfg.S3Endpoint,
		publicDomain:  cfg.S3PublicDomain,
		httpClient:    httpClient,
		presignClient: presignClient,
	}
}

func (s *StorageAdapter) Store(file *multipart.FileHeader, path string, isPublic bool) error {
	fileOpened, err := file.Open()
	if err != nil {
		return err
	}
	defer fileOpened.Close()

	contentType := file.Header.Get("Content-Type")
	return s.StoreFromReader(fileOpened, contentType, path, isPublic)
}

func (s *StorageAdapter) StoreFromReader(reader io.Reader, contentType string, path string, isPublic bool) error {
	if s.client == nil {
		return errors.New("s3 client is not initialized")
	}

	bucket := s.bucketPrivate
	if isPublic {
		bucket = s.bucketPublic
	}

	s3Key := filepath.ToSlash(path)
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	_, err := s.client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(s3Key),
		Body:        reader,
		ContentType: aws.String(contentType),
	})
	return err
}

func (s *StorageAdapter) Download(url string) ([]byte, string, error) {
	operation := func() (*http.Response, bool, error) {
		resp, err := s.httpClient.Get(url)
		if helper.ShouldRetryHTTP(resp, err) {
			if resp != nil {
				resp.Body.Close()
			}
			return nil, true, err
		}
		if err != nil {
			return nil, false, err
		}

		if resp.StatusCode != http.StatusOK {
			defer resp.Body.Close()
			return nil, false, fmt.Errorf("failed to download image, status code: %d", resp.StatusCode)
		}

		return resp, false, nil
	}

	resp, err := helper.RetryWithBackoff(operation, 3, 500*time.Millisecond)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}

	contentType := http.DetectContentType(data)
	return data, contentType, nil
}

func (s *StorageAdapter) Delete(path string, isPublic bool) error {
	if s.client == nil {
		return errors.New("s3 client is not initialized")
	}

	bucket := s.bucketPrivate
	if isPublic {
		bucket = s.bucketPublic
	}

	s3Key := filepath.ToSlash(path)
	_, err := s.client.DeleteObject(context.TODO(), &s3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(s3Key),
	})
	return err
}

func (s *StorageAdapter) GetPublicURL(path string) string {

	if s.publicDomain != "" {

		return fmt.Sprintf("%s/%s", s.publicDomain, filepath.ToSlash(path))
	}

	return fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", s.bucketPublic, s.region, filepath.ToSlash(path))
}

func (s *StorageAdapter) GetPresignedURL(path string, expiry time.Duration) (string, error) {
	if s.presignClient == nil {
		return "", errors.New("presign client is not initialized")
	}

	s3Key := filepath.ToSlash(path)
	req, err := s.presignClient.PresignGetObject(context.TODO(), &s3.GetObjectInput{
		Bucket: aws.String(s.bucketPrivate),
		Key:    aws.String(s3Key),
	}, func(opts *s3.PresignOptions) {
		opts.Expires = expiry
	})

	if err != nil {
		return "", err
	}

	return req.URL, nil
}

func (s *StorageAdapter) GetPresignedPutURL(path string, contentType string, contentLength int64, isPublic bool, expiry time.Duration) (string, map[string]string, error) {
	if s.presignClient == nil {
		return "", nil, errors.New("presign client is not initialized")
	}

	bucket := s.bucketPrivate
	if isPublic {
		bucket = s.bucketPublic
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	s3Key := filepath.ToSlash(path)
	req, err := s.presignClient.PresignPutObject(context.TODO(), &s3.PutObjectInput{
		Bucket:        aws.String(bucket),
		Key:           aws.String(s3Key),
		ContentType:   aws.String(contentType),
		ContentLength: aws.Int64(contentLength),
	}, func(opts *s3.PresignOptions) {
		opts.Expires = expiry
	})
	if err != nil {
		return "", nil, err
	}

	headers := map[string]string{"Content-Type": contentType}
	return req.URL, headers, nil
}

func (s *StorageAdapter) Head(path string, isPublic bool) (*StorageObjectInfo, error) {
	if s.client == nil {
		return nil, errors.New("s3 client is not initialized")
	}

	bucket := s.bucketPrivate
	if isPublic {
		bucket = s.bucketPublic
	}

	s3Key := filepath.ToSlash(path)
	resp, err := s.client.HeadObject(context.TODO(), &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(s3Key),
	})
	if err != nil {
		return nil, err
	}

	info := &StorageObjectInfo{}
	if resp.ContentLength != nil {
		info.Size = *resp.ContentLength
	}
	if resp.ContentType != nil {
		info.ContentType = *resp.ContentType
	}

	return info, nil
}
