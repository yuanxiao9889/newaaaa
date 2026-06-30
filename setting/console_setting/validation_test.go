package console_setting

import (
	"strings"
	"testing"
	"time"
)

func TestValidateAnnouncementsAllowsLongContent(t *testing.T) {
	longContent := strings.Repeat("公告", 300)
	payload := `[{"content":"` + longContent + `","publishDate":"` + time.Now().Format(time.RFC3339) + `","type":"default"}]`

	if err := validateAnnouncements(payload); err != nil {
		t.Fatalf("expected long announcement content to be valid, got %v", err)
	}
}

func TestValidateAnnouncementsStillLimitsExtra(t *testing.T) {
	extra := strings.Repeat("x", 201)
	payload := `[{"content":"ok","publishDate":"` + time.Now().Format(time.RFC3339) + `","type":"default","extra":"` + extra + `"}]`

	if err := validateAnnouncements(payload); err == nil {
		t.Fatal("expected overlong announcement extra to be rejected")
	}
}
