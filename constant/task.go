package constant

type TaskPlatform string

const (
	TaskPlatformSuno          TaskPlatform = "suno"
	TaskPlatformMidjourney                 = "mj"
	TaskPlatformInternalImage              = "internal_image"
)

const (
	SunoActionMusic  = "MUSIC"
	SunoActionLyrics = "LYRICS"

	TaskActionGenerate          = "generate"
	TaskActionTextGenerate      = "textGenerate"
	TaskActionFirstTailGenerate = "firstTailGenerate"
	TaskActionReferenceGenerate = "referenceGenerate"
	TaskActionRemix             = "remixGenerate"
	TaskActionImageGenerate     = "image_generate"
	TaskActionImageEdit         = "image_edit"
)

var SunoModel2Action = map[string]string{
	"suno_music":  SunoActionMusic,
	"suno_lyrics": SunoActionLyrics,
}
