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
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type S3Storage struct {
	scheme    string
	host      string
	accessKey string
	secretKey string
	bucket    string
	region    string
	client    *http.Client
}

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
	if err := s.EnsureBucket(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *S3Storage) EnsureBucket(ctx context.Context) error {
	req, err := s.newSignedRequest(ctx, http.MethodHead, "/"+s.bucket, nil, "")
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("minio head bucket: %w", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}

	putReq, err := s.newSignedRequest(ctx, http.MethodPut, "/"+s.bucket, nil, "")
	if err != nil {
		return err
	}
	putResp, err := s.client.Do(putReq)
	if err != nil {
		return fmt.Errorf("minio create bucket: %w", err)
	}
	_, _ = io.Copy(io.Discard, putResp.Body)
	putResp.Body.Close()
	if putResp.StatusCode != http.StatusOK && putResp.StatusCode != http.StatusCreated && putResp.StatusCode != http.StatusConflict {
		return fmt.Errorf("minio create bucket status %d", putResp.StatusCode)
	}
	return nil
}

func (s *S3Storage) Put(ctx context.Context, key string, data []byte, contentType string) error {
	path := "/" + s.bucket + "/" + strings.TrimLeft(key, "/")
	req, err := s.newSignedRequest(ctx, http.MethodPut, path, data, contentType)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("minio put object: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("minio put object status %d", resp.StatusCode)
	}
	return nil
}

func (s *S3Storage) Get(ctx context.Context, key string) (io.ReadCloser, string, int64, error) {
	path := "/" + s.bucket + "/" + strings.TrimLeft(key, "/")
	req, err := s.newSignedRequest(ctx, http.MethodGet, path, nil, "")
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
		return nil, "", 0, fmt.Errorf("minio get object status %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	size, _ := strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64)
	return resp.Body, ct, size, nil
}

func (s *S3Storage) Delete(ctx context.Context, key string) error {
	path := "/" + s.bucket + "/" + strings.TrimLeft(key, "/")
	req, err := s.newSignedRequest(ctx, http.MethodDelete, path, nil, "")
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("minio delete object: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("minio delete object status %d", resp.StatusCode)
	}
	return nil
}

func (s *S3Storage) newSignedRequest(ctx context.Context, method, rawPath string, body []byte, contentType string) (*http.Request, error) {
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")

	payloadHash := sha256Hex(body)
	canonicalURI := escapeS3Path(rawPath)

	u := fmt.Sprintf("%s://%s%s", s.scheme, s.host, canonicalURI)
	var bodyReader io.Reader
	if len(body) > 0 {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Host = s.host
	req.Header.Set("Host", s.host)
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
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var canonicalHeaders strings.Builder
	for _, k := range keys {
		canonicalHeaders.WriteString(k)
		canonicalHeaders.WriteByte(':')
		canonicalHeaders.WriteString(strings.TrimSpace(headers[k]))
		canonicalHeaders.WriteByte('\n')
	}
	signedHeaders := strings.Join(keys, ";")

	canonicalRequest := strings.Join([]string{
		method,
		canonicalURI,
		"",
		canonicalHeaders.String(),
		signedHeaders,
		payloadHash,
	}, "\n")

	credentialScope := fmt.Sprintf("%s/%s/s3/aws4_request", dateStamp, s.region)
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		credentialScope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	signingKey := deriveSigningKey(s.secretKey, dateStamp, s.region, "s3")
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))

	authHeader := fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		s.accessKey, credentialScope, signedHeaders, signature,
	)
	req.Header.Set("Authorization", authHeader)
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

type LocalDiskStorage struct {
	baseDir string
}

func NewLocalDiskStorage(baseDir string) (*LocalDiskStorage, error) {
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return nil, fmt.Errorf("create local storage dir: %w", err)
	}
	return &LocalDiskStorage{baseDir: baseDir}, nil
}

func (l *LocalDiskStorage) resolve(key string) (string, error) {
	clean := filepath.Clean(strings.TrimLeft(key, "/"))
	if strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("invalid storage key")
	}
	return filepath.Join(l.baseDir, clean), nil
}

func (l *LocalDiskStorage) Put(_ context.Context, key string, data []byte, _ string) error {
	full, err := l.resolve(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, data, 0o644)
}

func (l *LocalDiskStorage) Get(_ context.Context, key string) (io.ReadCloser, string, int64, error) {
	full, err := l.resolve(key)
	if err != nil {
		return nil, "", 0, err
	}
	f, err := os.Open(full)
	if err != nil {
		return nil, "", 0, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, "", 0, err
	}
	return f, "application/octet-stream", info.Size(), nil
}

func (l *LocalDiskStorage) Delete(_ context.Context, key string) error {
	full, err := l.resolve(key)
	if err != nil {
		return err
	}
	err = os.Remove(full)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
