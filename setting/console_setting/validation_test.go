package console_setting

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestValidateAnnouncementsContentLimit(t *testing.T) {
	publishDate := time.Now().Format(time.RFC3339)

	validContent := strings.Repeat("??", 250)
	validPayload := `[{"content":"` + validContent + `","publishDate":"` + publishDate + `","type":"default"}]`
	require.NoError(t, validateAnnouncements(validPayload))

	overlongContent := strings.Repeat("??", 251)
	overlongPayload := `[{"content":"` + overlongContent + `","publishDate":"` + publishDate + `","type":"default"}]`
	require.Error(t, validateAnnouncements(overlongPayload))
}

func TestValidateAnnouncementsStillLimitsExtra(t *testing.T) {
	extra := strings.Repeat("x", 201)
	payload := `[{"content":"ok","publishDate":"` + time.Now().Format(time.RFC3339) + `","type":"default","extra":"` + extra + `"}]`

	require.Error(t, validateAnnouncements(payload))
}
