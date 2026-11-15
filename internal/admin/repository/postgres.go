// Package repository implements the admin module's PostgreSQL (Gorm) queries and
// its Redis cache invalidation and event publishing.
package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/7fil/Hackathon-ShowcaseWall.git/internal/admin/service"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/pgarray"
)

// featuredLockKey serialises featured-mark changes so two Admins cannot trip the
// partial unique index at the same time.
const featuredLockKey int64 = 7301001

type workRow struct {
	ID          string              `gorm:"column:id;type:uuid;primaryKey"`
	OwnerName   string              `gorm:"column:owner_name;->"`
	Title       string              `gorm:"column:title"`
	Summary     string              `gorm:"column:summary"`
	Description string              `gorm:"column:description"`
	Benefits    string              `gorm:"column:benefits"`
	Category    string              `gorm:"column:category"`
	TechStack   pgarray.StringArray `gorm:"column:tech_stack;type:text[]"`
	TeamMembers pgarray.StringArray `gorm:"column:team_members;type:text[]"`
	DemoURL     *string             `gorm:"column:demo_url"`
	RepoURL     *string             `gorm:"column:repo_url"`
	Status      string              `gorm:"column:status"`
	IsFeatured  bool                `gorm:"column:is_featured"`
	CoverKey    string              `gorm:"column:cover_key"`
	PublishedAt *time.Time          `gorm:"column:published_at"`
	CreatedAt   time.Time           `gorm:"column:created_at"`
	UpdatedAt   time.Time           `gorm:"column:updated_at"`
}

func (workRow) TableName() string { return "works" }

func (m workRow) toDomain() *service.Work {
	return &service.Work{
		ID:          m.ID,
		OwnerName:   m.OwnerName,
		Title:       m.Title,
		Summary:     m.Summary,
		Description: m.Description,
		Benefits:    m.Benefits,
		Category:    m.Category,
		TechStack:   []string(m.TechStack),
		TeamMembers: []string(m.TeamMembers),
		DemoURL:     m.DemoURL,
		RepoURL:     m.RepoURL,
		Status:      m.Status,
		IsFeatured:  m.IsFeatured,
		CoverKey:    m.CoverKey,
		PublishedAt: m.PublishedAt,
		CreatedAt:   m.CreatedAt,
		UpdatedAt:   m.UpdatedAt,
	}
}

// WorkRepo implements service.WorkRepository on PostgreSQL.
type WorkRepo struct {
	db *gorm.DB
}

var _ service.WorkRepository = (*WorkRepo)(nil)

// NewWorkRepo returns the repository.
func NewWorkRepo(db *gorm.DB) *WorkRepo { return &WorkRepo{db: db} }

// base selects works joined with the owner's display name, skipping deleted rows.
func base(tx *gorm.DB) *gorm.DB {
	return tx.Table("works").
		Select("works.*, users.display_name AS owner_name").
		Joins("JOIN users ON users.id = works.owner_id").
		Where("works.deleted_at IS NULL")
}

// List returns every work (all owners), newest created first.
func (r *WorkRepo) List(ctx context.Context, f service.ListFilter, cursorTime *time.Time, cursorID string) ([]*service.Work, error) {
	q := base(r.db.WithContext(ctx))

	if f.Query != "" {
		pattern := "%" + escapeLike(f.Query) + "%"
		q = q.Where("(works.title ILIKE ? OR users.display_name ILIKE ?)", pattern, pattern)
	}
	if f.Status != "" {
		q = q.Where("works.status = ?", f.Status)
	}
	if cursorTime != nil && cursorID != "" {
		q = q.Where("(works.created_at < ? OR (works.created_at = ? AND works.id < ?))", *cursorTime, *cursorTime, cursorID)
	}

	var rows []workRow
	if err := q.Order("works.created_at DESC, works.id DESC").Limit(f.Limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]*service.Work, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.toDomain())
	}
	return out, nil
}

// Summary counts all non-deleted works by status and the featured mark.
func (r *WorkRepo) Summary(ctx context.Context) (service.DashboardSummary, error) {
	type summaryRow struct {
		TotalDraft       int `gorm:"column:total_draft"`
		TotalPublished   int `gorm:"column:total_published"`
		TotalUnpublished int `gorm:"column:total_unpublished"`
		TotalFeatured    int `gorm:"column:total_featured"`
	}
	var row summaryRow
	err := r.db.WithContext(ctx).Table("works").
		Select(`COUNT(*) FILTER (WHERE status = 'Draft') AS total_draft,
			COUNT(*) FILTER (WHERE status = 'Published') AS total_published,
			COUNT(*) FILTER (WHERE status = 'Unpublished') AS total_unpublished,
			COUNT(*) FILTER (WHERE status = 'Published' AND is_featured = TRUE) AS total_featured`).
		Where("deleted_at IS NULL").
		Scan(&row).Error
	if err != nil {
		return service.DashboardSummary{}, err
	}
	return service.DashboardSummary{
		TotalDraft:       row.TotalDraft,
		TotalPublished:   row.TotalPublished,
		TotalUnpublished: row.TotalUnpublished,
		TotalFeatured:    row.TotalFeatured,
	}, nil
}

// Publish is one conditional update on the current status, then a read in the
// same transaction.
func (r *WorkRepo) Publish(ctx context.Context, id string, now time.Time) (*service.Work, error) {
	return r.transition(ctx, id, func(tx *gorm.DB) *gorm.DB {
		return tx.Model(&workRow{}).
			Where("id = ? AND deleted_at IS NULL AND status IN ?", id, []string{service.StatusDraft, service.StatusUnpublished}).
			Updates(map[string]any{
				"status":       service.StatusPublished,
				"published_at": now,
				"updated_at":   now,
			})
	})
}

// Unpublish hides a Published work and clears its featured mark in one statement.
func (r *WorkRepo) Unpublish(ctx context.Context, id string, now time.Time) (*service.Work, error) {
	return r.transition(ctx, id, func(tx *gorm.DB) *gorm.DB {
		return tx.Model(&workRow{}).
			Where("id = ? AND deleted_at IS NULL AND status = ?", id, service.StatusPublished).
			Updates(map[string]any{
				"status":      service.StatusUnpublished,
				"is_featured": false,
				"updated_at":  now,
			})
	})
}

// transition runs a conditional update; zero affected rows means the work is
// missing (ErrNotFound) or in the wrong status (ErrStatusConflict).
func (r *WorkRepo) transition(ctx context.Context, id string, update func(tx *gorm.DB) *gorm.DB) (*service.Work, error) {
	var out *service.Work
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := update(tx)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return missOrConflict(tx, id)
		}
		w, err := find(tx, id)
		if err != nil {
			return err
		}
		out = w
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// SetFeatured sets or clears the mark. Only a Published work can carry it, and
// setting it clears the previous mark inside the same transaction.
func (r *WorkRepo) SetFeatured(ctx context.Context, id string, featured bool, now time.Time) (*service.Work, error) {
	var out *service.Work
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", featuredLockKey).Error; err != nil {
			return err
		}

		var cur workRow
		if err := tx.Table("works").Where("id = ? AND deleted_at IS NULL", id).Take(&cur).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return service.ErrNotFound
			}
			return err
		}
		if cur.Status != service.StatusPublished {
			return service.ErrStatusConflict
		}

		if featured {
			if err := tx.Model(&workRow{}).
				Where("is_featured = TRUE AND id <> ? AND deleted_at IS NULL", id).
				Updates(map[string]any{"is_featured": false, "updated_at": now}).Error; err != nil {
				return err
			}
		}
		res := tx.Model(&workRow{}).
			Where("id = ? AND deleted_at IS NULL AND status = ?", id, service.StatusPublished).
			Updates(map[string]any{"is_featured": featured, "updated_at": now})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return service.ErrStatusConflict
		}
		w, err := find(tx, id)
		if err != nil {
			return err
		}
		out = w
		return nil
	})
	if err != nil {
		if isUniqueViolation(err) {
			return nil, service.ErrStatusConflict
		}
		return nil, err
	}
	return out, nil
}

func find(tx *gorm.DB, id string) (*service.Work, error) {
	var row workRow
	if err := base(tx).Where("works.id = ?", id).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, service.ErrNotFound
		}
		return nil, err
	}
	return row.toDomain(), nil
}

func missOrConflict(tx *gorm.DB, id string) error {
	var n int64
	if err := tx.Model(&workRow{}).Where("id = ? AND deleted_at IS NULL", id).Count(&n).Error; err != nil {
		return err
	}
	if n == 0 {
		return service.ErrNotFound
	}
	return service.ErrStatusConflict
}

// isUniqueViolation recognises PostgreSQL error 23505. The database is opened with
// TranslateError, so it normally arrives as gorm.ErrDuplicatedKey; the message
// check is a fallback that avoids importing the driver.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "SQLSTATE 23505") || strings.Contains(msg, "duplicate key value")
}

// escapeLike escapes the LIKE metacharacters so user input is matched literally.
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}
