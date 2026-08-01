package extensions

import (
	"embed"
	"io/fs"
)

//go:embed */extension.json
var officialManifests embed.FS

func OfficialManifests() fs.FS {
	return officialManifests
}
