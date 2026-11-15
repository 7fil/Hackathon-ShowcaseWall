package service

import (
	"context"
	"errors"
	"io"
	"time"
)

const (
	StatusDraft       = "Draft"
	StatusPublished   = "Published"
	StatusUnpublished = "Unpublished"

	MinTitleRunes       = 3
	MaxTitleRunes       = 150
	MaxSummaryRunes     = 160
	MinDescriptionRunes = 10
	MaxDescriptionRunes = 10000
	MinBenefitsRunes    = 10
	MaxBenefitsRunes    = 5000
	MaxCategoryRunes    = 50
	MinListItems        = 1
	MaxListItems        = 10
	MaxListItemRunes    = 80
	MaxURLLength        = 500

	MaxCoverBytes    = 5 << 20 // 5 MB
	DefaultPageLimit = 20
	MaxPageLimit     = 20
	MaxExhibition    = 300

	GalleryCacheTTL = 60 * time.Second
	DetailCacheTTL  = 120 * time.Second
	SignedCoverTTL  = 10 * time.Minute

	EventWorkPublished   = "work.published"
	EventWorkUnpublished = "work.unpublished"
	EventWorkDeleted     = "work.deleted"
)

var (
	ErrNotFound       = errors.New("work not found")
	ErrStatusConflict = errors.New("work status conflict")
)

type Work struct {
	ID               string
	OwnerID          string
	OwnerName        string
	Title            string
	Summary          string
	Description      string
	Benefits         string
	Category         string
	TechStack        []string
	TeamMembers      []string
	DemoURL          *string
	RepoURL          *string
	Status           string
	IsFeatured       bool
	CoverKey         string
	CoverContentType string
	CoverSizeBytes   int64
	PublishedAt      *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type PublicWork struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Summary     string    `json:"summary"`
	Category    string    `json:"category"`
	TechStack   []string  `json:"tech_stack"`
	TeamMembers []string  `json:"team_members"`
	CoverURL    string    `json:"cover_url"`
	PublishedAt time.Time `json:"published_at"`
	IsFeatured  bool      `json:"is_featured"`
}

type PublicWorkDetail struct {
	PublicWork
	Description string  `json:"description"`
	Benefits    string  `json:"benefits"`
	DemoURL     *string `json:"demo_url"`
	RepoURL     *string `json:"repo_url"`
}

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

type DashboardSummary struct {
	TotalDraft       int `json:"total_draft"`
	TotalPublished   int `json:"total_published"`
	TotalUnpublished int `json:"total_unpublished"`
	TotalFeatured    int `json:"total_featured"`
}

type Facets struct {
	Categories []string `json:"categories"`
	TechStack  []string `json:"tech_stack"`
}

type PublicListFilter struct {
	Query     string
	Category  string
	TechStack string
	Cursor    string
	Limit     int
}

type ManagedListFilter struct {
	OwnerID string
	Query   string
	Status  string
	Cursor  string
	Limit   int
}

type PublicPage struct {
	Items      []PublicWork `json:"items"`
	NextCursor *string      `json:"next_cursor"`
}

type ManagedPage struct {
	Items      []ManagedWork `json:"items"`
	NextCursor *string       `json:"next_cursor"`
}

type CoverUpload struct {
	Data        []byte
	ContentType string
}

type CreateWorkInput struct {
	Title       string
	Summary     string
	Description string
	Benefits    string
	Category    string
	TechStack   []string
	TeamMembers []string
	DemoURL     string
	RepoURL     string
	Cover       *CoverUpload
}

type UpdateWorkInput struct {
	Title       string
	Summary     string
	Description string
	Benefits    string
	Category    string
	TechStack   []string
	TeamMembers []string
	DemoURL     string
	RepoURL     string
	Cover       *CoverUpload
}

type CoverStream struct {
	Body        io.ReadCloser
	ContentType string
	Size        int64
	ETag        string
}

type WsEvent struct {
	Type    string `json:"type"`
	Seq     int64  `json:"seq"`
	Payload any    `json:"payload"`
}

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

type WsIDPayload struct {
	ID string `json:"id"`
}

type WorkRepository interface {
	Create(ctx context.Context, w *Work) error
	FindByID(ctx context.Context, id string) (*Work, error)
	Update(ctx context.Context, w *Work) error
	SoftDelete(ctx context.Context, id string, deletedAt time.Time) error

	ListPublished(ctx context.Context, f PublicListFilter, cursorTime *time.Time, cursorID string) ([]*Work, error)
	FindFeatured(ctx context.Context) (*Work, error)
	ListExhibition(ctx context.Context, maxItems int) ([]*Work, error)
	GetFacets(ctx context.Context) (Facets, error)

	ListManaged(ctx context.Context, f ManagedListFilter, cursorTime *time.Time, cursorID string) ([]*Work, error)
	GetSummary(ctx context.Context, ownerID string) (DashboardSummary, error)

	Publish(ctx context.Context, id string, now time.Time) (*Work, error)
	Unpublish(ctx context.Context, id string, now time.Time) (*Work, error)
	SetFeatured(ctx context.Context, id string, featured bool, now time.Time) (*Work, error)
}

type ObjectStorage interface {
	Put(ctx context.Context, key string, data []byte, contentType string) error
	Get(ctx context.Context, key string) (body io.ReadCloser, contentType string, size int64, err error)
	Delete(ctx context.Context, key string) error
}

type WorkCache interface {
	Version(ctx context.Context) (int64, error)
	BumpVersion(ctx context.Context) error
	GetJSON(ctx context.Context, key string, dest any) (bool, error)
	SetJSON(ctx context.Context, key string, val any, ttl time.Duration) error
	Delete(ctx context.Context, keys ...string) error
}

type EventPublisher interface {
	PublishWorkEvent(ctx context.Context, eventType string, payload any) error
}