// Package adminseed turns existing accounts into Admins.
//
// It is the only way an Admin comes to exist. It is deliberately not an HTTP
// endpoint: it runs as a command inside the API container, so only someone who can
// run `docker exec` on the server can use it. The package has no database code of
// its own; the caller supplies a Store.
package adminseed

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/mail"
	"sort"
	"strings"
)

// Outcome is what happened to one email.
type Outcome string

const (
	Promoted     Outcome = "promoted"      // was a Contributor, is now Admin
	AlreadyAdmin Outcome = "already_admin" // nothing to change
	NotFound     Outcome = "not_found"     // no account with this email (it must register first)
)

// Store changes roles. PromoteToAdmin reports NotFound for an unknown email.
type Store interface {
	PromoteToAdmin(ctx context.Context, email string) (Outcome, error)
}

const maxEmailLen = 254

// ParseEmails splits args on commas and whitespace, lower-cases and de-duplicates them,
// and separates the valid emails from the rejected ones (input order is kept).
func ParseEmails(args []string) (valid, invalid []string) {
	seen := map[string]bool{}
	for _, arg := range args {
		for _, raw := range strings.FieldsFunc(arg, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n' }) {
			e := strings.ToLower(strings.TrimSpace(raw))
			if e == "" || seen[e] {
				continue
			}
			seen[e] = true
			if !validEmail(e) {
				invalid = append(invalid, raw)
				continue
			}
			valid = append(valid, e)
		}
	}
	return valid, invalid
}

func validEmail(e string) bool {
	if len(e) > maxEmailLen {
		return false
	}
	a, err := mail.ParseAddress(e)
	// Address must round-trip exactly: this rejects "Name <a@b.c>" forms and trailing junk.
	return err == nil && a.Address == e && a.Name == ""
}

// Usage is printed when the command is run without emails.
const Usage = `usage: /app/seed <email> [<email> ...]

Makes the given registered users Admin. Several emails can be given at once,
separated by spaces or commas. Each person must already have an account
(registered or signed in with Google once); unknown emails are reported and skipped.

  docker exec -it showcase-api /app/seed alice@example.com bob@example.com
`

// Run promotes every email and writes one human-readable line per email to out.
// Each change is also written to audit. It returns the process exit code:
// 0 when every email ended up as Admin, 1 when any was invalid, unknown or failed,
// 2 for a usage error.
func Run(ctx context.Context, store Store, args []string, out io.Writer, audit *slog.Logger) int {
	emails, invalid := ParseEmails(args)
	if len(emails) == 0 && len(invalid) == 0 {
		fmt.Fprint(out, Usage)
		return 2
	}

	failed := 0
	for _, raw := range invalid {
		fmt.Fprintf(out, "SKIP  %s  not a valid email address\n", raw)
		failed++
	}

	var promoted, already, missing []string
	for _, email := range emails {
		outcome, err := store.PromoteToAdmin(ctx, email)
		switch {
		case err != nil:
			fmt.Fprintf(out, "FAIL  %s  %v\n", email, err)
			audit.Error("audit: admin promotion failed", "email", email, "error", err.Error())
			failed++
		case outcome == Promoted:
			fmt.Fprintf(out, "OK    %s  is now Admin\n", email)
			audit.Warn("audit: admin role granted", "email", email, "via", "seed command")
			promoted = append(promoted, email)
		case outcome == AlreadyAdmin:
			fmt.Fprintf(out, "OK    %s  was already Admin\n", email)
			already = append(already, email)
		case outcome == NotFound:
			fmt.Fprintf(out, "SKIP  %s  no account with this email: they must register first\n", email)
			audit.Warn("audit: admin promotion skipped, no such account", "email", email)
			missing = append(missing, email)
			failed++
		default:
			err := errors.New("unexpected outcome " + string(outcome))
			fmt.Fprintf(out, "FAIL  %s  %v\n", email, err)
			failed++
		}
	}

	sort.Strings(missing)
	fmt.Fprintf(out, "\n%d promoted, %d already admin, %d not found, %d invalid/failed\n",
		len(promoted), len(already), len(missing), failed-len(missing))
	if len(promoted) > 0 {
		fmt.Fprintln(out, "They get the Admin role at their next sign-in or token refresh (within 15 minutes).")
	}
	if failed > 0 {
		return 1
	}
	return 0
}
