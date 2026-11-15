package service

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func validateWorkFields(title, summary, description, benefits, category, demoURL, repoURL string, techStack, teamMembers []string) (
	normTitle, normSummary, normDesc, normBenefits, normCategory string,
	normTech, normTeam []string,
	normDemo, normRepo *string,
	fields map[string]string,
) {
	fields = map[string]string{}

	normTitle = strings.TrimSpace(title)
	if n := utf8.RuneCountInString(normTitle); n < MinTitleRunes {
		fields["title"] = "Judul minimal 3 karakter"
	} else if n > MaxTitleRunes {
		fields["title"] = "Judul maksimal 150 karakter"
	}

	normSummary = strings.TrimSpace(summary)
	if n := utf8.RuneCountInString(normSummary); n == 0 {
		fields["summary"] = "Ringkasan wajib diisi"
	} else if n > MaxSummaryRunes {
		fields["summary"] = "Ringkasan maksimal 160 karakter"
	}

	normDesc = strings.TrimSpace(description)
	if n := utf8.RuneCountInString(normDesc); n < MinDescriptionRunes {
		fields["description"] = "Deskripsi minimal 10 karakter"
	} else if n > MaxDescriptionRunes {
		fields["description"] = "Deskripsi terlalu panjang"
	}

	normBenefits = strings.TrimSpace(benefits)
	if n := utf8.RuneCountInString(normBenefits); n < MinBenefitsRunes {
		fields["benefits"] = "Manfaat karya minimal 10 karakter"
	} else if n > MaxBenefitsRunes {
		fields["benefits"] = "Manfaat karya terlalu panjang"
	}

	normCategory = strings.TrimSpace(category)
	if n := utf8.RuneCountInString(normCategory); n == 0 {
		fields["category"] = "Kategori wajib diisi"
	} else if n > MaxCategoryRunes {
		fields["category"] = "Kategori maksimal 50 karakter"
	}

	normTech = cleanStringList(techStack)
	if len(normTech) < MinListItems {
		fields["tech_stack"] = "Tech stack minimal 1 item"
	} else if len(normTech) > MaxListItems {
		fields["tech_stack"] = "Tech stack maksimal 10 item"
	} else {
		for _, item := range normTech {
			if utf8.RuneCountInString(item) > MaxListItemRunes {
				fields["tech_stack"] = "Setiap item tech stack maksimal 80 karakter"
				break
			}
		}
	}

	normTeam = cleanStringList(teamMembers)
	if len(normTeam) < MinListItems {
		fields["team_members"] = "Anggota tim minimal 1 nama"
	} else if len(normTeam) > MaxListItems {
		fields["team_members"] = "Anggota tim maksimal 10 nama"
	} else {
		for _, item := range normTeam {
			if utf8.RuneCountInString(item) > MaxListItemRunes {
				fields["team_members"] = "Nama anggota tim maksimal 80 karakter"
				break
			}
		}
	}

	if d := strings.TrimSpace(demoURL); d != "" {
		if !validHTTPURL(d) {
			fields["demo_url"] = "URL demo harus berupa tautan http atau https yang valid"
		} else {
			normDemo = &d
		}
	}

	if r := strings.TrimSpace(repoURL); r != "" {
		if !validHTTPURL(r) {
			fields["repo_url"] = "URL repository harus berupa tautan http atau https yang valid"
		} else {
			normRepo = &r
		}
	}

	return
}

func cleanStringList(raw []string) []string {
	out := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, s := range raw {
		v := strings.TrimSpace(s)
		if v == "" {
			continue
		}
		key := strings.ToLower(v)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, v)
	}
	return out
}

func validHTTPURL(raw string) bool {
	if len(raw) > MaxURLLength {
		return false
	}
	u, err := url.ParseRequestURI(raw)
	if err != nil {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return false
	}
	return u.Host != "" && strings.Contains(u.Host, ".")
}

func sniffCover(c *CoverUpload) (contentType, ext string, msg string, ok bool) {
	if c == nil || len(c.Data) == 0 {
		return "", "", "File cover wajib diunggah", false
	}
	if len(c.Data) > MaxCoverBytes {
		return "", "", "Ukuran file cover maksimal 5MB", false
	}
	detected := http.DetectContentType(c.Data)
	switch detected {
	case "image/jpeg":
		return "image/jpeg", ".jpg", "", true
	case "image/png":
		return "image/png", ".png", "", true
	case "image/webp":
		return "image/webp", ".webp", "", true
	default:
		return "", "", "Format cover harus berupa gambar JPG, PNG, atau WebP", false
	}
}

func ValidStatus(s string) bool {
	return s == StatusDraft || s == StatusPublished || s == StatusUnpublished
}

func NewUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

type pageCursor struct {
	Time time.Time `json:"t"`
	ID   string    `json:"id"`
}

func encodeCursor(t time.Time, id string) string {
	b, _ := json.Marshal(pageCursor{Time: t.UTC(), ID: id})
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(raw string) (*time.Time, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, "", nil
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		b, err = base64.URLEncoding.DecodeString(raw)
		if err != nil {
			return nil, "", err
		}
	}
	var c pageCursor
	if err := json.Unmarshal(b, &c); err != nil || c.ID == "" || c.Time.IsZero() {
		return nil, "", fmt.Errorf("invalid cursor")
	}
	return &c.Time, c.ID, nil
}

func stableCoverURL(workID string) string {
	return "/api/v1/media/works/" + workID + "/cover"
}

func signCoverURL(secret []byte, workID, coverKey string, exp time.Time) string {
	expUnix := exp.Unix()
	sig := computeCoverHMAC(secret, workID, coverKey, expUnix)
	return fmt.Sprintf("/api/v1/media/works/%s/cover?exp=%d&sig=%s", workID, expUnix, sig)
}

func verifyCoverSignature(secret []byte, workID, coverKey, expStr, sigStr string, now time.Time) bool {
	if expStr == "" || sigStr == "" {
		return false
	}
	expUnix, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return false
	}
	if now.Unix() > expUnix {
		return false
	}
	expected := computeCoverHMAC(secret, workID, coverKey, expUnix)
	return hmac.Equal([]byte(expected), []byte(sigStr))
}

func computeCoverHMAC(secret []byte, workID, coverKey string, expUnix int64) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(fmt.Sprintf("%s\x00%s\x00%d", workID, coverKey, expUnix)))
	return hex.EncodeToString(m.Sum(nil))
}