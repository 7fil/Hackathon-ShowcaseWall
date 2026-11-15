package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/7fil/Hackathon-ShowcaseWall.git/internal/auth/service"
)

// ResendMailer sends email through the Resend HTTP API.
type ResendMailer struct {
	apiKey string
	from   string
	client *http.Client
}

func NewResendMailer(apiKey, from string) *ResendMailer {
	return &ResendMailer{apiKey: apiKey, from: from, client: &http.Client{Timeout: 10 * time.Second}}
}

func (m *ResendMailer) Send(ctx context.Context, msg service.Message) error {
	body, err := json.Marshal(map[string]any{
		"from": m.from, "to": []string{msg.To},
		"subject": msg.Subject, "text": msg.Text, "html": msg.HTML,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.resend.com/emails", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+m.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.client.Do(req)
	if err != nil {
		return fmt.Errorf("resend request: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Only the status is reported: the response may echo request data.
		return fmt.Errorf("resend responded with status %d", resp.StatusCode)
	}
	return nil
}

// ConsoleMailer prints codes to stdout for local development (DEV_PRINT_OTP=true).
// Never enabled in production; the config refuses that combination.
type ConsoleMailer struct{}

func (ConsoleMailer) Send(_ context.Context, msg service.Message) error {
	_, err := fmt.Fprintf(os.Stdout, "\n[DEV EMAIL] to=%s subject=%q\n%s\n\n", msg.To, msg.Subject, msg.Text)
	return err
}
