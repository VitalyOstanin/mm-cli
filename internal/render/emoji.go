package render

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mattermost/mattermost/server/public/model"
)

// EmojiUnicode is the Unicode text of the standard emoji called name ("+1" is
// the thumbs-up character), including skin-tone modifiers and variation
// selectors as the server's table gives them. It returns "" for a name the
// table does not know, which is how a custom emoji shows up.
func EmojiUnicode(name string) string {
	code, ok := model.SystemEmojis[name]
	if !ok {
		return ""
	}
	var b strings.Builder
	for _, part := range strings.Split(code, "-") {
		n, err := strconv.ParseUint(part, 16, 32)
		// The range check keeps the conversion lossless and drops code
		// points that are not characters (surrogates, above U+10FFFF).
		if err != nil || n > unicode.MaxRune || !utf8.ValidRune(rune(n)) {
			return ""
		}
		b.WriteRune(rune(n))
	}
	return b.String()
}

// EmojiLabel is how text output shows an emoji: the character followed by the
// name for a standard one ("👍 :+1:"), so that the name stays greppable and
// matches what people type; ":name:" alone for a custom one.
func EmojiLabel(name string) string {
	if u := EmojiUnicode(name); u != "" {
		return u + " :" + name + ":"
	}
	return ":" + name + ":"
}
