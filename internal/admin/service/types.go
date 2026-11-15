// Package service holds the Admin moderation rules: listing every work, the
// system-wide summary, publish, unpublish and the featured mark. After each
// committed change it invalidates the public cache and publishes realtime events.
package service

import (
	"context"
	"errors"
	"time"
)

const (
	StatusDraft       = "Draft"
	StatusPublished   = "Published"
	StatusUnpublished = "Unpublished"

	DefaultPageLimit = 20
	MaxPageLimit     = 20

	EventWorkPublished   = "work.published"
	EventWorkUnpublished = "work.unpublished"
)

var (
	// ErrNotFound means the work does not exist or was deleted.
	ErrNotFound = errors.New("work not found")
	// ErrStatusConflict means the work is not in a status that allows the action.
	ErrStatusConflict = errors.New("work status conflict")
)

// Work is the repository's view of a works row joined with its owner's name.
type Work struct {
	ID          string
	OwnerName   string
	Title       string
	Summary     string
	Description string
	Benefits    string
	Category    string
	TechStack   []string
	TeamMembers []string
	DemoURL     *string
	RepoURL     *string
	Status      string
	IsFeatured  bool
	CoverKey    string
	PublishedAt *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ManagedWork is the management DTO (PRD section 7): public detail fields plus
// status, owner name and timestamps.
type ManagedWork struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Summary     string     `json:"summary"`
	Description string     `json:"description"`
	Benefits    string     `json:"benefits"`
	Category    string     `json:"category"`
	TechStack   []string   `json:"tech_stack"`
	TeamMembers []string   `json:"team_members"`
	CoverURL    string     `json:"cover_url"`
	DemoURL     *string    `json:"demo_url"`
	RepoURL     *string    `json:"repo_url"`
	Status      string     `json:"status"`
	IsFeatured  bool       `json:"is_featured"`
	OwnerName   string     `json:"owner_name"`
	PublishedAt *time.Time `json:"published_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// DashboardSummary counts works by status and the featured mark.
type DashboardSummary struct {
	TotalDraft       int `json:"total_draft"`
	TotalPublished   int `json:"total_published"`
	TotalUnpublished int `json:"total_unpublished"`
	TotalFeatured    int `json:"total_featured"`
}

// ListFilter is the Admin table filter. Query matches title or owner name.
type ListFilter struct {
	Query  string
	Status string
	Cursor string
	Limit  int
}

// ManagedPage is one page of the Admin table.
type ManagedPage struct {
	Items      []ManagedWork `json:"items"`
	NextCursor *string       `json:"next_cursor"`
}

// WsEvent is the realtime envelope { type, seq, payload }.
type WsEvent struct {
	Type    string `json:"type"`
	Seq     int64  `json:"seq"`
	Payload any    `json:"payload"`
}

// WsPublishedPayload carries public card fields only: no email, owner id,
// status or featured mark.
type WsPublishedPayload struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Summary     string    `json:"summary"`
	Category    string    `json:"category"`
	TechStack   []string  `json:"tech_stack"`
	TeamMembers []string  `json:"team_members"`
	CoverURL    string    `json:"cover_url"`
	PublishedAt time.Time `json:"published_at"`
}

// WsIDPayload is the payload of work.unpublished.
type WsIDPayload struct {
	ID string `json:"id"`
}

// WorkRepository is implemented on PostgreSQL. Every status change is one
// conditional update, so two Admins cannot both act on the same work: the loser
// gets ErrStatusConflict.
type WorkRepository interface {
	// List returns up to f.Limit works, newest created first, after the cursor.
	List(ctx context.Context, f ListFilter, cursorTime *time.Time, cursorID string) ([]*Work, error)
	// Summary counts all works that are not deleted.
	Summary(ctx context.Context) (DashboardSummary, error)
	// Publish moves Draft or Unpublished to Published and sets published_at.
	Publish(ctx context.Context, id string, now time.Time) (*Work, error)
	// Unpublish moves Published to Unpublished and clears the featured mark in the same transaction.
	Unpublish(ctx context.Context, id string, now time.Time) (*Work, error)
	// SetFeatured sets or clears the mark of a Published work; setting it clears the previous one in the same transaction.
	SetFeatured(ctx context.Context, id string, featured bool, now time.Time) (*Work, error)
}

// Cache invalidates the public gallery cache.
type Cache interface {
	BumpVersion(ctx context.Context) error
	Delete(ctx context.Context, keys ...string) error
}

// EventPublisher pushes a realtime event to the public channel.
type EventPublisher interface {
	PublishWorkEvent(ctx context.Context, eventType string, payload any) error
}

// CoverURLs is implemented by the media service. Signed URLs are built per
// response and never cached.
type CoverURLs interface {
	CoverURL(workID, coverKey string, published bool) string
	StableCoverURL(workID string) string
}
