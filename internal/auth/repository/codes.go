package repository

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/7fil/Hackathon-ShowcaseWall.git/internal/auth/service"
)

// CodeRepo keeps pending registrations and login codes in Redis, where they expire on their own.
// Keys never hold a plaintext code: only an HMAC of it.
type CodeRepo struct{ rdb *redis.Client }

func NewCodeRepo(rdb *redis.Client) *CodeRepo { return &CodeRepo{rdb: rdb} }

func pendingKey(email string) string { return "auth:pending:register:" + email }

func codeKey(p service.Purpose, email string) string { return "auth:otp:" + string(p) + ":" + email }

func attemptsKey(p service.Purpose, email string) string {
	return "auth:otp_attempts:" + string(p) + ":" + email
}

type pendingJSON struct {
	Name         string `json:"name"`
	PasswordHash string `json:"password_hash"`
}

func (r *CodeRepo) SavePending(ctx context.Context, email string, p service.PendingRegistration, ttl time.Duration) error {
	b, err := json.Marshal(pendingJSON{Name: p.Name, PasswordHash: p.PasswordHash})
	if err != nil {
		return err
	}
	return r.rdb.Set(ctx, pendingKey(email), b, ttl).Err()
}

func (r *CodeRepo) GetPending(ctx context.Context, email string) (*service.PendingRegistration, error) {
	b, err := r.rdb.Get(ctx, pendingKey(email)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, service.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var pj pendingJSON
	if err := json.Unmarshal(b, &pj); err != nil {
		return nil, err
	}
	return &service.PendingRegistration{Name: pj.Name, PasswordHash: pj.PasswordHash}, nil
}

func (r *CodeRepo) DeletePending(ctx context.Context, email string) error {
	return r.rdb.Del(ctx, pendingKey(email)).Err()
}

// SaveCode replaces any earlier code and resets the attempt counter.
func (r *CodeRepo) SaveCode(ctx context.Context, p service.Purpose, email, codeHash string, ttl time.Duration) error {
	pipe := r.rdb.TxPipeline()
	pipe.Set(ctx, codeKey(p, email), codeHash, ttl)
	pipe.Del(ctx, attemptsKey(p, email))
	_, err := pipe.Exec(ctx)
	return err
}

func (r *CodeRepo) HasCode(ctx context.Context, p service.Purpose, email string) (bool, error) {
	n, err := r.rdb.Exists(ctx, codeKey(p, email)).Result()
	return n > 0, err
}

func (r *CodeRepo) ConsumeCode(ctx context.Context, p service.Purpose, email, codeHash string, maxAttempts int) (service.CodeStatus, error) {
	ck, ak := codeKey(p, email), attemptsKey(p, email)

	stored, err := r.rdb.Get(ctx, ck).Result()
	if errors.Is(err, redis.Nil) {
		return service.CodeMissing, nil
	}
	if err != nil {
		return service.CodeMissing, err
	}

	attempts, err := r.rdb.Incr(ctx, ak).Result()
	if err != nil {
		return service.CodeMissing, err
	}
	if attempts == 1 {
		// The counter lives no longer than the code; SaveCode also resets it.
		if err := r.rdb.Expire(ctx, ak, service.OTPTTL).Err(); err != nil {
			return service.CodeMissing, err
		}
	}
	if attempts > int64(maxAttempts) {
		_ = r.rdb.Del(ctx, ck, ak).Err()
		return service.CodeExhausted, nil
	}

	if subtle.ConstantTimeCompare([]byte(stored), []byte(codeHash)) != 1 {
		if attempts == int64(maxAttempts) {
			_ = r.rdb.Del(ctx, ck, ak).Err() // last attempt spent: the code is void
			return service.CodeExhausted, nil
		}
		return service.CodeMismatch, nil
	}

	// DEL reports how many keys it removed, so only one concurrent request can win.
	n, err := r.rdb.Del(ctx, ck).Result()
	if err != nil {
		return service.CodeMissing, err
	}
	_ = r.rdb.Del(ctx, ak).Err()
	if n == 0 {
		return service.CodeMissing, nil
	}
	return service.CodeOK, nil
}
