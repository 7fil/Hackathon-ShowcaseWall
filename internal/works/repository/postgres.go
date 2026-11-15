package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/7fil/Hackathon-ShowcaseWall.git/internal/works/service"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/pgarray"
)

type workModel struct {
	ID               string              `gorm:"column:id;type:uuid;primaryKey"`
	OwnerID          string              `gorm:"column:owner_id;type:uuid"`
	OwnerName        string              `gorm:"column:owner_name;->"`
	Title            string              `gorm:"column:title"`
	Summary          string              `gorm:"column:summary"`
	Description      string              `gorm:"column:description"`
	Benefits         string              `gorm:"column:benefits"`
	Category         string              `gorm:"column:category"`
	TechStack        pgarray.StringArray `gorm:"column:tech_stack;type:text[]"`
	TeamMembers      pgarray.StringArray `gorm:"column:team_members;type:text[]"`
	DemoURL          *string             `gorm:"column:demo_url"`
	RepoURL          *string             `gorm:"column:repo_url"`
	Status           string              `gorm:"column:status"`
	IsFeatured       bool                `gorm:"column:is_featured"`
	CoverKey         string              `gorm:"column:cover_key"`
	CoverContentType string              `gorm:"column:cover_content_type"`
	CoverSizeBytes   int64               `gorm:"column:cover_size_bytes"`
	PublishedAt      *time.Time          `gorm:"column:published_at"`
	CreatedAt        time.Time           `gorm:"column:created_at"`
	UpdatedAt        time.Time           `gorm:"column:updated_at"`
	DeletedAt        *time.Time          `gorm:"column:deleted_at"`
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
		TechStack:        pgarray.StringArray(w.TechStack),
		TeamMembers:      pgarray.StringArray(w.TeamMembers),
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
	if !looksLikeUUID(id) {
		return nil, service.ErrNotFound // avoids PostgreSQL's uuid cast error on a malformed id
	}
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
			"tech_stack":         pgarray.StringArray(w.TechStack),
			"team_members":       pgarray.StringArray(w.TeamMembers),
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

func toDomainSlice(rows []workModel) []*service.Work {
	out := make([]*service.Work, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.toDomain())
	}
	return out
}

func escapeILIKE(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
				return false
			}
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Screenshots

type screenshotModel struct {
	ID          string `gorm:"column:id;type:uuid;primaryKey"`
	WorkID      string `gorm:"column:work_id;type:uuid"`
	ObjectKey   string `gorm:"column:object_key"`
	Position    int    `gorm:"column:position"`
	ContentType string `gorm:"column:content_type"`
	SizeBytes   int64  `gorm:"column:size_bytes"`
}

func (screenshotModel) TableName() string { return "work_screenshots" }

func (m screenshotModel) toDomain() service.Screenshot {
	return service.Screenshot{
		ID: m.ID, WorkID: m.WorkID, ObjectKey: m.ObjectKey,
		Position: m.Position, ContentType: m.ContentType, SizeBytes: m.SizeBytes,
	}
}

func (r *WorkRepo) ListScreenshots(ctx context.Context, workID string) ([]service.Screenshot, error) {
	if !looksLikeUUID(workID) {
		return nil, nil
	}
	var rows []screenshotModel
	if err := r.db.WithContext(ctx).Where("work_id = ?", workID).Order("position ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]service.Screenshot, 0, len(rows))
	for _, m := range rows {
		out = append(out, m.toDomain())
	}
	return out, nil
}

func (r *WorkRepo) FindScreenshot(ctx context.Context, workID, imageID string) (*service.Screenshot, error) {
	if !looksLikeUUID(workID) || !looksLikeUUID(imageID) {
		return nil, service.ErrScreenshotNotFound
	}
	var m screenshotModel
	err := r.db.WithContext(ctx).Where("id = ? AND work_id = ?", imageID, workID).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, service.ErrScreenshotNotFound
		}
		return nil, err
	}
	sh := m.toDomain()
	return &sh, nil
}

// AddScreenshot locks the work row so two uploads cannot both pass the limit check.
func (r *WorkRepo) AddScreenshot(ctx context.Context, sh *service.Screenshot, max int) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var locked string
		if err := tx.Raw("SELECT id FROM works WHERE id = ? AND deleted_at IS NULL FOR UPDATE", sh.WorkID).Scan(&locked).Error; err != nil {
			return err
		}
		if locked == "" {
			return service.ErrNotFound
		}
		var next struct {
			Count int
			Next  int
		}
		if err := tx.Raw("SELECT COUNT(*) AS count, COALESCE(MAX(position), 0) + 1 AS next FROM work_screenshots WHERE work_id = ?", sh.WorkID).Scan(&next).Error; err != nil {
			return err
		}
		if next.Count >= max {
			return service.ErrScreenshotLimit
		}
		sh.Position = next.Next
		m := screenshotModel{
			ID: sh.ID, WorkID: sh.WorkID, ObjectKey: sh.ObjectKey,
			Position: sh.Position, ContentType: sh.ContentType, SizeBytes: sh.SizeBytes,
		}
		return tx.Create(&m).Error
	})
}

func (r *WorkRepo) DeleteScreenshot(ctx context.Context, workID, imageID string) error {
	if !looksLikeUUID(workID) || !looksLikeUUID(imageID) {
		return service.ErrScreenshotNotFound
	}
	res := r.db.WithContext(ctx).Where("id = ? AND work_id = ?", imageID, workID).Delete(&screenshotModel{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return service.ErrScreenshotNotFound
	}
	return nil
}
