package cli

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
)

// emojiImages gives the image file of a custom emoji for a notification. A
// run asks the server at most twice per emoji (the lookup by name and the
// image download), whatever the outcome: failures are remembered too, and
// the file is kept in the user cache directory for later runs. Every failure
// only means a notification without an image.
type emojiImages struct {
	s *Session
	// cacheRoot is the user cache directory, read once; "" when it is unknown.
	cacheRoot string
	mu        sync.Mutex
	done      map[string]string // emoji name -> file path, "" after a failure
}

// userCacheDir is os.UserCacheDir; tests replace it, as the variable that
// moves it differs between systems.
var userCacheDir = os.UserCacheDir

func newEmojiImages(s *Session) *emojiImages {
	root, err := userCacheDir()
	if err != nil {
		slog.Debug("emoji cache directory", "err", err)
	}
	return &emojiImages{s: s, cacheRoot: root, done: map[string]string{}}
}

var unsafePathChars = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// path returns the image file of the custom emoji name, or "" without one.
// The lock spans the requests: notifications come one at a time, and a second
// reaction with the same emoji must wait for the first lookup, not repeat it.
func (e *emojiImages) path(ctx context.Context, name string) string {
	if e == nil {
		return ""
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if p, ok := e.done[name]; ok {
		return p
	}
	p := e.fetch(ctx, name)
	// A stop of the run is not a verdict on the emoji.
	if ctx.Err() == nil {
		e.done[name] = p
	}
	return p
}

func (e *emojiImages) fetch(ctx context.Context, name string) string {
	s := e.s
	em, _, err := s.Client.GetEmojiByName(ctx, name)
	if err != nil || em == nil || em.Id == "" {
		slog.DebugContext(ctx, "emoji lookup failed", "name", name, "err", err)
		return ""
	}
	if e.cacheRoot == "" {
		return ""
	}
	dir := filepath.Join(e.cacheRoot, "mm-cli", "emoji", unsafePathChars.ReplaceAllString(s.Name, "_"))
	id := unsafePathChars.ReplaceAllString(em.Id, "_")
	if hit, _ := filepath.Glob(filepath.Join(dir, id+".*")); len(hit) > 0 {
		return hit[0]
	}
	img, _, err := s.Client.GetEmojiImage(ctx, em.Id)
	if err != nil || len(img) == 0 {
		slog.DebugContext(ctx, "emoji image download failed", "name", name, "err", err)
		return ""
	}
	if len(img) > maxEmojiImageSize {
		slog.DebugContext(ctx, "emoji image too large", "name", name, "size", len(img))
		return ""
	}
	// Desktop notifiers choose how to decode by the extension, so a body that
	// is not an image the server accepts is not saved under an image name.
	ext := ""
	switch http.DetectContentType(img) {
	case "image/png":
		ext = ".png"
	case "image/gif":
		ext = ".gif"
	case "image/jpeg":
		ext = ".jpg"
	default:
		slog.DebugContext(ctx, "emoji image is not an image", "name", name)
		return ""
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		slog.DebugContext(ctx, "emoji image not cached", "name", name, "err", err)
		return ""
	}
	file := filepath.Join(dir, id+ext)
	if err := writeFileAtomic(file, img); err != nil {
		slog.DebugContext(ctx, "emoji image not cached", "name", name, "err", err)
		return ""
	}
	return file
}

// maxEmojiImageSize is the Mattermost server's limit for a custom emoji file
// (512 KiB); a larger body is not an emoji image.
const maxEmojiImageSize = 512 * 1024

// writeFileAtomic writes through a temporary file in the same directory and
// renames it, so a reader of the cache never sees a half-written image and a
// failed write leaves nothing under the final name.
func writeFileAtomic(file string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(file), ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(name, 0o600)
	}
	if err == nil {
		err = os.Rename(name, file)
	}
	if err != nil {
		_ = os.Remove(name)
	}
	return err
}
