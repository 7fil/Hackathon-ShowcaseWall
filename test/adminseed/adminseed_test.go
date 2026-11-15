package adminseed

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"
)

type fakeStore struct {
	roles map[string]string // email -> role
	err   map[string]error
	calls []string
}

func (f *fakeStore) PromoteToAdmin(_ context.Context, email string) (Outcome, error) {
	f.calls = append(f.calls, email)
	if e := f.err[email]; e != nil {
		return "", e
	}
	role, ok := f.roles[email]
	switch {
	case !ok:
		return NotFound, nil
	case role == "Admin":
		return AlreadyAdmin, nil
	}
	f.roles[email] = "Admin"
	return Promoted, nil
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestParseEmails(t *testing.T) {
	valid, invalid := ParseEmails([]string{"A@Example.com,b@example.com", " a@example.com ", "nope", "Name <c@example.com>", "d@example.com;e@example.com"})
	wantValid := []string{"a@example.com", "b@example.com", "d@example.com", "e@example.com"}
	if !reflect.DeepEqual(valid, wantValid) {
		t.Errorf("valid = %v, want %v", valid, wantValid)
	}
	if len(invalid) != 3 { // "nope", "Name", "<c@example.com>" (split on space)
		t.Errorf("invalid = %v", invalid)
	}
	if v, i := ParseEmails([]string{strings.Repeat("a", 250) + "@example.com"}); len(v) != 0 || len(i) != 1 {
		t.Error("over-long email must be rejected")
	}
}

func TestRunPromotesSeveralAtOnce(t *testing.T) {
	st := &fakeStore{roles: map[string]string{"a@x.com": "Contributor", "b@x.com": "Contributor", "c@x.com": "Admin"}}
	var out bytes.Buffer
	code := Run(context.Background(), st, []string{"a@x.com", "b@x.com,c@x.com"}, &out, quiet())
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, out.String())
	}
	if st.roles["a@x.com"] != "Admin" || st.roles["b@x.com"] != "Admin" {
		t.Errorf("roles not updated: %v", st.roles)
	}
	if !strings.Contains(out.String(), "was already Admin") || !strings.Contains(out.String(), "2 promoted, 1 already admin") {
		t.Errorf("summary wrong:\n%s", out.String())
	}
}

func TestRunDoesNotCreateUnknownAccounts(t *testing.T) {
	st := &fakeStore{roles: map[string]string{"a@x.com": "Contributor"}}
	var out bytes.Buffer
	code := Run(context.Background(), st, []string{"a@x.com", "ghost@x.com"}, &out, quiet())
	if code != 1 {
		t.Errorf("an unknown email must make the command fail, exit = %d", code)
	}
	if st.roles["a@x.com"] != "Admin" {
		t.Error("the valid email must still be promoted when another one fails")
	}
	if _, created := st.roles["ghost@x.com"]; created || !strings.Contains(out.String(), "must register first") {
		t.Errorf("unknown email handling wrong:\n%s", out.String())
	}
}

func TestRunContinuesAfterStoreError(t *testing.T) {
	st := &fakeStore{
		roles: map[string]string{"a@x.com": "Contributor", "b@x.com": "Contributor"},
		err:   map[string]error{"a@x.com": errors.New("db down")},
	}
	var out bytes.Buffer
	if code := Run(context.Background(), st, []string{"a@x.com", "b@x.com"}, &out, quiet()); code != 1 {
		t.Errorf("exit = %d", code)
	}
	if st.roles["b@x.com"] != "Admin" {
		t.Error("one failure must not stop the others")
	}
}

func TestRunInvalidEmailNeverReachesStore(t *testing.T) {
	st := &fakeStore{roles: map[string]string{}}
	var out bytes.Buffer
	if code := Run(context.Background(), st, []string{"not-an-email"}, &out, quiet()); code != 1 {
		t.Errorf("exit = %d", code)
	}
	if len(st.calls) != 0 {
		t.Errorf("store was called with %v", st.calls)
	}
}

func TestRunWithoutArgsPrintsUsage(t *testing.T) {
	var out bytes.Buffer
	if code := Run(context.Background(), &fakeStore{}, nil, &out, quiet()); code != 2 {
		t.Errorf("exit = %d", code)
	}
	if !strings.Contains(out.String(), "docker exec -it showcase-api /app/seed") {
		t.Errorf("usage missing:\n%s", out.String())
	}
}

func TestAuditLineDoesNotExposeFullEmail(t *testing.T) {
	// The audit logger masks "email" attributes; here we only check that Run logs the grant.
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	st := &fakeStore{roles: map[string]string{"a@x.com": "Contributor"}}
	Run(context.Background(), st, []string{"a@x.com"}, io.Discard, log)
	if !strings.Contains(buf.String(), "admin role granted") {
		t.Errorf("no audit line: %s", buf.String())
	}
}
