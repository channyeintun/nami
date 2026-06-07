package tui

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/channyeintun/nami/internal/ipc"
)

type promptImage struct {
	payload ipc.ImageInputPayload
}

func parseImageReferences(text string, nextID int) ([]promptImage, string) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, text
	}
	if image, ok := parseDataURLImage(trimmed, nextID); ok {
		return []promptImage{{payload: image}}, fmt.Sprintf("[Image #%d]", nextID)
	}
	if image, ok := parsePathImage(trimmed, nextID); ok {
		return []promptImage{{payload: image}}, fmt.Sprintf("[Image #%d]", nextID)
	}
	return nil, text
}

func parseDataURLImage(value string, id int) (ipc.ImageInputPayload, bool) {
	if !strings.HasPrefix(value, "data:image/") {
		return ipc.ImageInputPayload{}, false
	}
	mediaEnd := strings.Index(value, ";base64,")
	if mediaEnd <= len("data:") {
		return ipc.ImageInputPayload{}, false
	}
	dataStart := mediaEnd + len(";base64,")
	data := strings.TrimSpace(value[dataStart:])
	if data == "" {
		return ipc.ImageInputPayload{}, false
	}
	if _, err := base64.StdEncoding.DecodeString(data); err != nil {
		return ipc.ImageInputPayload{}, false
	}
	return ipc.ImageInputPayload{
		ID:        id,
		Data:      data,
		MediaType: strings.TrimPrefix(value[:mediaEnd], "data:"),
	}, true
}

func parsePathImage(value string, id int) (ipc.ImageInputPayload, bool) {
	path := strings.Trim(value, "\"'")
	if !filepath.IsAbs(path) || !isImagePath(path) {
		return ipc.ImageInputPayload{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ipc.ImageInputPayload{}, false
	}
	return ipc.ImageInputPayload{
		ID:         id,
		Data:       base64.StdEncoding.EncodeToString(data),
		MediaType:  mediaTypeForPath(path),
		Filename:   filepath.Base(path),
		SourcePath: path,
	}, true
}

func isImagePath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	default:
		return false
	}
}

func mediaTypeForPath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return "image/png"
	}
}
