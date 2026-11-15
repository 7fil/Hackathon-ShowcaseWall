// Package repository implements the media object stores: MinIO (S3 compatible,
// path-style, SigV4 over plain net/http) and a local-disk fallback for development.
package repository

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/7fil/Hackathon-ShowcaseWall.git/internal/media/service"
)

// S3Storage talks to MinIO inside the Docker network. Credentials come from the
// environment and never leave this type.
type S3Storage struct {
	scheme    string
	host      string
	accessKey string
	secretKey string
	bucket    string
	region    string
	client    *http.Client
}

var _ service.ObjectStorage = (*S3Storage)(nil)

// NewS3Storage connects to the endpoint (host:port) and makes sure the private
// bucket exists.
func NewS3Storage(ctx context.Context, endpoint, accessKey, secretKey, bucket string, useSSL bool) (*S3Storage, error) {
	endpoint = strings.TrimSpace(endpoint)
	endpoint = strings.TrimPrefix(endpoint, "http://")
	endpoint = strings.TrimPrefix(endpoint, "https://")
	endpoint = strings.TrimRight(endpoint, "/")
	scheme := "http"
	if useSSL {
		scheme = "https"
	}
	s := &S3Storage{
		scheme:    scheme,
		host:      endpoint,
		accessKey: accessKey,
		secretKey: secretKey,
		bucket:    bucket,
		region:    "us-east-1",
		client:    &http.Client{Timeout: 30 * time.Second},
	}
	if err := s.ensureBucket(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *S3Storage) ensureBucket(ctx context.Context) error {
	status, err := s.do(ctx, http.MethodHead, "/"+s.bucket, nil, "")
	if err != nil {
		return fmt.Errorf("minio head bucket: %w", err)
	}
	if status == http.StatusOK {
		return nil
	}
	status, err = s.do(ctx, http.MethodPut, "/"+s.bucket, nil, "")
	if err != nil {
		return fmt.Errorf("minio create bucket: %w", err)
	}
	if status != http.StatusOK && status != http.StatusCreated && status != http.StatusConflict {
		return fmt.Errorf("minio create bucket status %d", status)
	}
	return nil
}

// do sends a request whose response body is not needed.
func (s *S3Storage) do(ctx context.Context, method, path string, body []byte, contentType string) (int, error) {
	req, err := s.newSignedRequest(ctx, method, path, body, contentType)
	if err != nil {
		return 0, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	return resp.StatusCode, nil
}

func (s *S3Storage) objectPath(key string) string {
	return "/" + s.bucket + "/" + strings.TrimLeft(key, "/")
}

// Put uploads one object.
func (s *S3Storage) Put(ctx context.Context, key string, data []byte, contentType string) error {
	status, err := s.do(ctx, http.MethodPut, s.objectPath(key), data, contentType)
	if err != nil {
		return fmt.Errorf("minio put object: %w", err)
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("minio put object status %d", status)
	}
	return nil
}

// Get opens one object. The caller must close the returned body.
func (s *S3Storage) Get(ctx context.Context, key string) (io.ReadCloser, string, int64, error) {
	req, err := s.newSignedRequest(ctx, http.MethodGet, s.objectPath(key), nil, "")
	if err != nil {
		return nil, "", 0, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, "", 0, fmt.Errorf("minio get object: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			return nil, "", 0, service.ErrObjectNotFound
		}
		return nil, "", 0, fmt.Errorf("minio get object status %d", resp.StatusCode)
	}
	size, _ := strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64)
	return resp.Body, resp.Header.Get("Content-Type"), size, nil
}

// Delete removes one object; a missing object counts as success.
func (s *S3Storage) Delete(ctx context.Context, key string) error {
	status, err := s.do(ctx, http.MethodDelete, s.objectPath(key), nil, "")
	if err != nil {
		return fmt.Errorf("minio delete object: %w", err)
	}
	if status != http.StatusNoContent && status != http.StatusOK && status != http.StatusNotFound {
		return fmt.Errorf("minio delete object status %d", status)
	}
	return nil
}

// newSignedRequest builds an AWS Signature V4 request (service s3).
func (s *S3Storage) newSignedRequest(ctx context.Context, method, rawPath string, body []byte, contentType string) (*http.Request, error) {
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")

	payloadHash := sha256Hex(body)
	canonicalURI := escapeS3Path(rawPath)

	var bodyReader io.Reader
	if len(body) > 0 {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.scheme+"://"+s.host+canonicalURI, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Host = s.host
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if len(body) > 0 {
		req.ContentLength = int64(len(body))
	}

	headers := map[string]string{
		"host":                 s.host,
		"x-amz-content-sha256": payloadHash,
		"x-amz-date":           amzDate,
	}
	if contentType != "" {
		headers["content-type"] = contentType
	}
	names := make([]string, 0, len(headers))
	for k := range headers {
		names = append(names, k)
	}
	sort.Strings(names)

	var canonicalHeaders strings.Builder
	for _, k := range names {
		canonicalHeaders.WriteString(k)
		canonicalHeaders.WriteByte(':')
		canonicalHeaders.WriteString(strings.TrimSpace(headers[k]))
		canonicalHeaders.WriteByte('\n')
	}
	signedHeaders := strings.Join(names, ";")

	canonicalRequest := strings.Join([]string{
		method,
		canonicalURI,
		"", // no query string
		canonicalHeaders.String(),
		signedHeaders,
		payloadHash,
	}, "\n")

	scope := fmt.Sprintf("%s/%s/s3/aws4_request", dateStamp, s.region)
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	signature := hex.EncodeToString(hmacSHA256(deriveSigningKey(s.secretKey, dateStamp, s.region, "s3"), []byte(stringToSign)))
	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		s.accessKey, scope, signedHeaders, signature,
	))
	return req, nil
}

func escapeS3Path(p string) string {
	parts := strings.Split(p, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	res := strings.Join(parts, "/")
	if !strings.HasPrefix(res, "/") {
		res = "/" + res
	}
	return res
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key, data []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(data)
	return m.Sum(nil)
}

func deriveSigningKey(secret, dateStamp, region, serviceName string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), []byte(dateStamp))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(serviceName))
	return hmacSHA256(kService, []byte("aws4_request"))
}
