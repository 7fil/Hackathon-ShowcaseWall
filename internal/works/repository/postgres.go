// Package repository implements PostgreSQL (Gorm), Redis caching & pub/sub,
// and MinIO S3-compatible object storage for the works module.
package repository

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/7fil/Hackathon-ShowcaseWall.git/internal/works/service"
)

type stringArray []string

func (a stringArray) Value() (driver.Value, error) {
	if len(a) == 0 {
		return "{}", nil
	}
	elems := make([]string, len(a))
	for i, s := range a {
		escaped := strings.ReplaceAll(s, `\`, `\`)
		escaped = strings.ReplaceAll(escaped, `"`, `"`)
		elems[i] = `"` + escaped + `"`
	}
	return "{" + strings.Join(elems, ",") + "}", nil
}

func (a *stringArray) Scan(src any) error {
	if src == nil {
		*a = []string{}
		return nil
	}
	var raw string
	switch v := src.(type) {
	case string:
		raw = v
	case []byte:
		raw = string(v)
	default:
		return fmt.Errorf("unsupported TEXT[] scan type %T", src)
	}
	*a = parsePGArray(raw)
	return nil
}

func parsePGArray(s string) []string {
	s = strings.TrimSpace(s)
	if len(s) < 2 || s[0] != '{' || s[len(s)-1] != '}' {
		return []string{}
	}
	inner := s[1 : len(s)-1]
	if inner == "" {
		return []string{}
	}
	var out []string
	var cur strings.Builder
	inQuotes := false
	escaped := false
	for i := 0; i < len(inner); i++ {
		ch := inner[i]
		if escaped {
			cur.WriteByte(ch)
			escaped = false
			continue
		}
		if ch == '\' {
			escaped = true
			continue
		}
		if ch == '"' {
			inQuotes = !inQuotes
			continue
		}
		if ch == ',' && !inQuotes {
			out = append(out, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteByte(ch)
	}
	out = append(out, cur.String())
	return out
}

type workModel struct {
	ID               string      `gorm:"column:id;type:uuid;primaryKey"`
	OwnerID          string      `gorm:"column:owner_id;type:uuid"`
	OwnerName        string      `gorm:"column:owner_name;->"`
	Title            string      `gorm:"column:title"`
	Summary          string      `gorm:"column:summary"`
	Description      string      `gorm:"column:description"`
	Benefits         string      `gorm:"column:benefits"`
	Category         string      `gorm:"column:category"`
	TechStack        stringArray `gorm:"column:tech_stack;type:text[]"`
	TeamMembers      stringArray `gorm:"column:team_members;type:text[]"`
	DemoURL          *string     `gorm:"column:demo_url"`
	RepoURL          *string     `gorm:"column:repo_url"`
	Status           string      `gorm:"column:status"`
	IsFeatured       bool        `gorm:"column:is_featured"`
	CoverKey         string      `gorm:"column:cover_key"`
	CoverContentType string      `gorm:"column:cover_content_type"`
	CoverSizeBytes   int64       `gorm:"column:cover_size_bytes"`
	PublishedAt      *time.Time  `gorm:"column:published_at"`
	CreatedAt        time.Time   `gorm:"column:created_at"`
	UpdatedAt        time.Time   `gorm:"column:updated_at"`
	DeletedAt        *time.Time  `gorm:"column:deleted_at"`
}

func (workModel) TableName() string { return "works" }

func (m workModel) toDomain() *service.Work {
	return &service.Work{
		ID:               m.ID,
		OwnerID:          m.OwnerID,
		OwnerName:        m.OwnerName,
		Title:            m.Title,
		Summary:          m.Summary,
		Description:      m.Description,
		Benefits:         m.Benefits,
		Category:         m.Category,
		TechStack:        []string(m.TechStack),
		TeamMembers:      []string(m.TeamMembers),
		DemoURL:          m.DemoURL,
		RepoURL:          m.RepoURL,
		Status:           m.Status,
		IsFeatured:       m.IsFeatured,
		CoverKey:         m.CoverKey,
		CoverContentType: m.CoverContentType,
		CoverSizeBytes:   m.CoverSizeBytes,
		PublishedAt:      m.PublishedAt,
		CreatedAt:        m.CreatedAt,
		UpdatedAt:        m.UpdatedAt,
	}
}

func newWorkModel(w *service.Work) workModel {
	return workModel{
		ID:               w.ID,
		OwnerID:          w.OwnerID,
		Title:            w.Title,
		Summary:          w.Summary,
		Description:      w.Description,
		Benefits:         w.Benefits,
		Category:         w.Category,
		TechStack:        stringArray(w.TechStack),
		TeamMembers:      stringArray(w.TeamMembers),
		DemoURL:          w.DemoURL,
		RepoURL:          w.RepoURL,
		Status:           w.Status,
		IsFeatured:       w.IsFeatured,
		CoverKey:         w.CoverKey,
		CoverContentType: w.CoverContentType,
		CoverSizeBytes:   w.CoverSizeBytes,
		PublishedAt:      w.PublishedAt,
		CreatedAt:        w.CreatedAt,
		UpdatedAt:        w.UpdatedAt,
	}
}

type WorkRepo struct {
	db *gorm.DB
}

func NewWorkRepo(db *gorm.DB) *WorkRepo {
	return &WorkRepo{db: db}
}

func (r *WorkRepo) baseQuery(ctx context.Context) *gorm.DB {
	return r.db.WithContext(ctx).
		Table("works").
		Select("works.*, users.display_name AS owner_name").
		Joins("JOIN users ON users.id = works.owner_id").
		Where("works.deleted_at IS NULL")
}

func (r *WorkRepo) Create(ctx context.Context, w *service.Work) error {
	m := newWorkModel(w)
	return r.db.WithContext(ctx).Create(&m).Error
}

func (r *WorkRepo) FindByID(ctx context.Context, id string) (*service.Work, error) {
	var m workModel
	err := r.baseQuery(ctx).Where("works.id = ?", id).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, service.ErrNotFound
		}
		return nil, err
	}
	return m.toDomain(), nil
}

func (r *WorkRepo) Update(ctx context.Context, w *service.Work) error {
	res := r.db.WithContext(ctx).Model(&workModel{}).
		Where("id = ? AND deleted_at IS NULL", w.ID).
		Updates(map[string]any{
			"title":              w.Title,
			"summary":            w.Summary,
			"description":        w.Description,
			"benefits":           w.Benefits,
			"category":           w.Category,
			"tech_stack":         stringArray(w.TechStack),
			"team_members":       stringArray(w.TeamMembers),
			"demo_url":           w.DemoURL,
			"repo_url":           w.RepoURL,
			"cover_key":          w.CoverKey,
			"cover_content_type": w.CoverContentType,
			"cover_size_bytes":   w.CoverSizeBytes,
			"updated_at":         w.UpdatedAt,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return service.ErrNotFound
	}
	return nil
}

func (r *WorkRepo) SoftDelete(ctx context.Context, id string, deletedAt time.Time) error {
	res := r.db.WithContext(ctx).Model(&workModel{}).
		Where("id = ? AND deleted_at IS NULL", id).
		Updates(map[string]any{
			"is_featured": false,
			"deleted_at":  deletedAt,
			"updated_at":  deletedAt,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return service.ErrNotFound
	}
	return nil
}

func (r *WorkRepo) ListPublished(ctx context.Context, f service.PublicListFilter, cursorTime *time.Time, cursorID string) ([]*service.Work, error) {
	q := r.baseQuery(ctx).Where("works.status = ?", service.StatusPublished)

	if f.Query != "" {
		q = q.Where("works.title ILIKE ?", "%"+escapeILIKE(f.Query)+"%")
	}
	if f.Category != "" {
		q = q.Where("LOWER(works.category) = LOWER(?)", f.Category)
	}
	if f.TechStack != "" {
		q = q.Where("EXISTS (SELECT 1 FROM unnest(works.tech_stack) AS ts WHERE LOWER(ts) = LOWER(?))", f.TechStack)
	}
	if cursorTime != nil && cursorID != "" {
		q = q.Where("(works.published_at < ? OR (works.published_at = ? AND works.id < ?))", *cursorTime, *cursorTime, cursorID)
	}

	var rows []workModel
	if err := q.Order("works.published_at DESC, works.id DESC").Limit(f.Limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	return toDomainSlice(rows), nil
}

func (r *WorkRepo) FindFeatured(ctx context.Context) (*service.Work, error) {
	var m workModel
	err := r.baseQuery(ctx).
		Where("works.status = ? AND works.is_featured = TRUE", service.StatusPublished).
		First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, service.ErrNotFound
		}
		return nil, err
	}
	return m.toDomain(), nil
}

func (r *WorkRepo) ListExhibition(ctx context.Context, maxItems int) ([]*service.Work, error) {
	var rows []workModel
	err := r.baseQuery(ctx).
		Where("works.status = ?", service.StatusPublished).
		Order("LOWER(works.title) ASC, works.id ASC").
		Limit(maxItems).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return toDomainSlice(rows), nil
}

func (r *WorkRepo) GetFacets(ctx context.Context) (service.Facets, error) {
	var categories []string
	err := r.db.WithContext(ctx).
		Raw(`SELECT DISTINCT category FROM works WHERE status = 'Published' AND deleted_at IS NULL ORDER BY category ASC`).
		Scan(&categories).Error
	if err != nil {
		return service.Facets{}, err
	}

	var techStack []string
	err = r.db.WithContext(ctx).
		Raw(`SELECT DISTINCT ts FROM works, unnest(works.tech_stack) AS ts WHERE works.status = 'Published' AND works.deleted_at IS NULL ORDER BY ts ASC`).
		Scan(&techStack).Error
	if err != nil {
		return service.Facets{}, err
	}

	return service.Facets{
		Categories: categories,
		TechStack:  techStack,
	}, nil
}

func (r *WorkRepo) ListManaged(ctx context.Context, f service.ManagedListFilter, cursorTime *time.Time, cursorID string) ([]*service.Work, error) {
	q := r.baseQuery(ctx)

	if f.OwnerID != "" {
		q = q.Where("works.owner_id = ?", f.OwnerID)
		if f.Query != "" {
			q = q.Where("works.title ILIKE ?", "%"+escapeILIKE(f.Query)+"%")
		}
	} else if f.Query != "" {
		pattern := "%" + escapeILIKE(f.Query) + "%"
		q = q.Where("(works.title ILIKE ? OR users.display_name ILIKE ?)", pattern, pattern)
	}

	if f.Status != "" {
		q = q.Where("works.status = ?", f.Status)
	}
	if cursorTime != nil && cursorID != "" {
		q = q.Where("(works.created_at < ? OR (works.created_at = ? AND works.id < ?))", *cursorTime, *cursorTime, cursorID)
	}

	var rows []workModel
	if err := q.Order("works.created_at DESC, works.id DESC").Limit(f.Limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	return toDomainSlice(rows), nil
}

func (r *WorkRepo) GetSummary(ctx context.Context, ownerID string) (service.DashboardSummary, error) {
	type summaryRow struct {
		TotalDraft       int `gorm:"column:total_draft"`
		TotalPublished   int `gorm:"column:total_published"`
		TotalUnpublished int `gorm:"column:total_unpublished"`
		TotalFeatured    int `gorm:"column:total_featured"`
	}
	var row summaryRow
	q := r.db.WithContext(ctx).Table("works").
		Select(`
			COUNT(*) FILTER (WHERE status = 'Draft') AS total_draft,
			COUNT(*) FILTER (WHERE status = 'Published') AS total_published,
			COUNT(*) FILTER (WHERE status = 'Unpublished') AS total_unpublished,
			COUNT(*) FILTER (WHERE status = 'Published' AND is_featured = TRUE) AS total_featured
		`).
		Where("deleted_at IS NULL")
	if ownerID != "" {
		q = q.Where("owner_id = ?", ownerID)
	}
	if err := q.Scan(&row).Error; err != nil {
		return service.DashboardSummary{}, err
	}
	return service.DashboardSummary{
		TotalDraft:       row.TotalDraft,
		TotalPublished:   row.TotalPublished,
		TotalUnpublished: row.TotalUnpublished,
		TotalFeatured:    row.TotalFeatured,
	}, nil
}

func (r *WorkRepo) Publish(ctx context.Context, id string, now time.Time) (*service.Work, error) {
	res := r.db.WithContext(ctx).Model(&workModel{}).
		Where("id = ? AND deleted_at IS NULL AND status IN ?", id, []string{service.StatusDraft, service.StatusUnpublished}).
		Updates(map[string]any{
			"status":       service.StatusPublished,
			"published_at": now,
			"updated_at":   now,
		})
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		if _, err := r.FindByID(ctx, id); err != nil {
			return nil, err
		}
		return nil, service.ErrStatusConflict
	}
	return r.FindByID(ctx, id)
}

func (r *WorkRepo) Unpublish(ctx context.Context, id string, now time.Time) (*service.Work, error) {
	res := r.db.WithContext(ctx).Model(&workModel{}).
		Where("id = ? AND deleted_at IS NULL AND status = ?", id, service.StatusPublished).
		Updates(map[string]any{
			"status":      service.StatusUnpublished,
			"is_featured": false,
			"updated_at":  now,
		})
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		if _, err := r.FindByID(ctx, id); err != nil {
			return nil, err
		}
		return nil, service.ErrStatusConflict
	}
	return r.FindByID(ctx, id)
}

func (r *WorkRepo) SetFeatured(ctx context.Context, id string, featured bool, now time.Time) (*service.Work, error) {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var m workModel
		if err := tx.Where("id = ? AND deleted_at IS NULL", id).First(&m).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return service.ErrNotFound
			}
			return err
		}
		if m.Status != service.StatusPublished {
			return service.ErrStatusConflict
		}

		if featured {
			if err := tx.Model(&workModel{}).
				Where("is_featured = TRUE AND id <> ? AND deleted_at IS NULL", id).
				Updates(map[string]any{"is_featured": false, "updated_at": now}).Error; err != nil {
				return err
			}
		}

		res := tx.Model(&workModel{}).
			Where("id = ? AND deleted_at IS NULL AND status = ?", id, service.StatusPublished).
			Updates(map[string]any{"is_featured": featured, "updated_at": now})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return service.ErrStatusConflict
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return r.FindByID(ctx, id)
}

func toDomainSlice(rows []workModel) []*service.Work {
	out := make([]*service.Work, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.toDomain())
	}
	return out
}

func escapeILIKE(s string) string {
	s = strings.ReplaceAll(s, `\`, `\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}
