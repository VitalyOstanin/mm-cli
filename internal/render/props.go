package render

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// Embed is a piece of content the server derived from a post (link preview,
// image, quoted post). Data is kept as the server sent it.
type Embed struct {
	Type string `json:"type"`
	URL  string `json:"url,omitempty"`
	Data any    `json:"data,omitempty"`
}

// Priority is the priority label of a post and its notification options.
type Priority struct {
	// Priority is "", "important" or "urgent".
	Priority                string `json:"priority"`
	RequestedAck            bool   `json:"requested_ack"`
	PersistentNotifications bool   `json:"persistent_notifications"`
}

// Acknowledgement is one user's acknowledgement of a post with a requested
// acknowledgement.
type Acknowledgement struct {
	UserID         string `json:"user_id"`
	User           string `json:"user"`
	AcknowledgedAt string `json:"acknowledged_at"`
}

// interactiveKeys are the props that hold interactive payloads the text
// output cannot show.
var interactiveKeys = []string{model.PostPropsMmBlocks, model.PostPropsBlockKitBlocks, model.PostPropsAdaptiveCards}

const (
	// priorityImportant has no constant in the model.
	priorityImportant = "important"
	// propCard is the props key of a card post; the model has no constant.
	propCard = "card"
)

// quoteSnippetLength is how many characters of a quoted post text output shows.
const quoteSnippetLength = 100

func propsOf(p *model.Post) map[string]any {
	src := p.GetProps()
	if len(src) == 0 {
		return nil
	}
	return maps.Clone(src)
}

func embedsOf(meta *model.PostMetadata) []Embed {
	if meta == nil {
		return nil
	}
	var out []Embed
	for _, e := range meta.Embeds {
		if e != nil {
			out = append(out, Embed{Type: string(e.Type), URL: e.URL, Data: e.Data})
		}
	}
	return out
}

func priorityOf(meta *model.PostMetadata) *Priority {
	if meta == nil || meta.Priority == nil {
		return nil
	}
	pr := &Priority{}
	if meta.Priority.Priority != nil {
		pr.Priority = *meta.Priority.Priority
	}
	if meta.Priority.RequestedAck != nil {
		pr.RequestedAck = *meta.Priority.RequestedAck
	}
	if meta.Priority.PersistentNotifications != nil {
		pr.PersistentNotifications = *meta.Priority.PersistentNotifications
	}
	if *pr == (Priority{}) {
		return nil
	}
	return pr
}

func acknowledgementsOf(meta *model.PostMetadata, names map[string]string) []Acknowledgement {
	if meta == nil {
		return nil
	}
	var out []Acknowledgement
	for _, a := range meta.Acknowledgements {
		if a != nil {
			out = append(out, Acknowledgement{UserID: a.UserId, User: cmp.Or(names[a.UserId], a.UserId), AcknowledgedAt: RFC3339(a.AcknowledgedAt)})
		}
	}
	return out
}

// quoteNamesOf maps the authors of quoted posts to their names, so that text
// output can show them; the JSON keeps only the embeds.
func quoteNamesOf(embeds []Embed, names map[string]string) map[string]string {
	var out map[string]string
	for _, e := range embeds {
		id, _ := previewOf(e)
		if id == "" || names[id] == "" {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		out[id] = names[id]
	}
	return out
}

// extraUserIDs returns the ids of the users who acknowledged p and of the
// authors of the posts it quotes.
func extraUserIDs(p *model.Post) []string {
	if p.Metadata == nil {
		return nil
	}
	var ids []string
	for _, a := range p.Metadata.Acknowledgements {
		if a != nil {
			ids = append(ids, a.UserId)
		}
	}
	for _, e := range p.Metadata.Embeds {
		if e == nil {
			continue
		}
		if id, _ := previewOf(Embed{Type: string(e.Type), Data: e.Data}); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// asMap converts embed data to a generic map, whether it arrived decoded from
// JSON or as a typed value; anything else gives nil.
func asMap(v any) map[string]any {
	switch x := v.(type) {
	case nil:
		return nil
	case map[string]any:
		return x
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}

// anyString reads a value that may be a string or, in damaged data, another
// scalar, which is formatted rather than dropped.
func anyString(m map[string]any, key string) string {
	switch s := m[key].(type) {
	case string:
		return s
	case nil:
		return ""
	default:
		return fmt.Sprint(s)
	}
}

// previewOf returns the author id and the message of a permalink embed.
func previewOf(e Embed) (userID, message string) {
	if e.Type != string(model.PostEmbedPermalink) {
		return "", ""
	}
	post := asMap(asMap(e.Data)["post"])
	return anyString(post, "user_id"), anyString(post, "message")
}

// attachmentsOf parses props.attachments; elements that do not parse are
// skipped.
func attachmentsOf(props map[string]any) []*model.MessageAttachment {
	switch v := props[model.PostPropsAttachments].(type) {
	case []*model.MessageAttachment:
		return v
	case []any:
		var out []*model.MessageAttachment
		for _, el := range v {
			raw, err := json.Marshal(el)
			if err != nil {
				continue
			}
			var a model.MessageAttachment
			if json.Unmarshal(raw, &a) == nil {
				out = append(out, &a)
			}
		}
		return out
	}
	return nil
}

func propTrue(props map[string]any, key string) bool {
	switch v := props[key].(type) {
	case string:
		return v == "true"
	case bool:
		return v
	}
	return false
}

// stringProp reads a prop that must be a string; any other type counts as
// absent, unlike anyString.
func stringProp(props map[string]any, key string) string {
	s, _ := props[key].(string)
	return s
}

func propPresent(props map[string]any, key string) bool {
	switch v := props[key].(type) {
	case nil:
		return false
	case string:
		return v != ""
	case []any:
		return len(v) > 0
	case map[string]any:
		return len(v) > 0
	}
	return true
}

// cardText is the text of a card post: props.card when it is a string.
func cardText(props map[string]any) string {
	return strings.TrimSpace(stringProp(props, propCard))
}

// PlainText is the text of a post for places that show one line of it, such
// as notifications. A post posted by a bot often has an empty message and
// the content in an attachment, so it falls back to the first attachment's
// pretext, title, text or fallback, then to the card text.
func PlainText(p *model.Post) string {
	if strings.TrimSpace(p.Message) != "" {
		return p.Message
	}
	props := p.GetProps()
	for _, a := range attachmentsOf(props) {
		if a == nil {
			continue
		}
		for _, s := range []string{a.Pretext, a.Title, a.Text, a.Fallback} {
			if strings.TrimSpace(s) != "" {
				return s
			}
		}
	}
	return cardText(props)
}

// authorLabel is the author part of the header line: @name, with the override
// name a bot or webhook posted under.
func authorLabel(p Post) string {
	who := "@" + cmp.Or(p.User.Username, p.User.ID)
	if o := stringProp(p.Props, model.PostPropsOverrideUsername); o != "" {
		who += fmt.Sprintf(" as %q", oneLine(o))
	}
	return who
}

// headerFlags returns the header suffix after edited/pinned.
func headerFlags(p Post) string {
	var b strings.Builder
	if propTrue(p.Props, model.PostPropsFromBot) {
		b.WriteString(", bot")
	}
	if propTrue(p.Props, model.PostPropsFromWebhook) {
		b.WriteString(", webhook")
	}
	if p.Type != "" {
		b.WriteString(", type " + oneLine(p.Type))
	}
	if p.Priority != nil {
		if p.Priority.Priority == model.PostPriorityUrgent || p.Priority.Priority == priorityImportant {
			b.WriteString(", " + p.Priority.Priority)
		}
		if p.Priority.RequestedAck {
			b.WriteString(", acknowledgement requested")
		}
		if p.Priority.PersistentNotifications {
			b.WriteString(", persistent notifications")
		}
	}
	return b.String()
}

// writeBody writes the message and what hangs off it: attachments, interactive
// payload markers, the card and embeds.
func writeBody(b *strings.Builder, p Post, truncate int) {
	if msg := shorten(p.Message, truncate); msg != "" {
		b.WriteString(msg)
		b.WriteString("\n")
	}
	atts := p.attachments
	if atts == nil {
		atts = attachmentsOf(p.Props)
	}
	for _, a := range atts {
		if a != nil {
			writeAttachment(b, a, truncate)
		}
	}
	for _, k := range interactiveKeys {
		if propPresent(p.Props, k) {
			fmt.Fprintf(b, "  [interactive %s] see \"props\" in --json\n", k)
		}
	}
	if c := cardText(p.Props); c != "" {
		b.WriteString("  [card]\n")
		writeIndented(b, shorten(c, truncate), "    ")
	}
	for _, e := range p.Embeds {
		writeEmbed(b, e, p.quoteNames)
	}
}

func writeIndented(b *strings.Builder, s, indent string) {
	for _, line := range strings.Split(s, "\n") {
		b.WriteString(indent)
		b.WriteString(line)
		b.WriteString("\n")
	}
}

const (
	attachmentIndent   = "    "
	continuationIndent = "      "
)

// oneLine replaces line breaks with spaces, for values printed on a single
// line: a break there would start a line outside the indented block, which
// could pass for the header of another message.
func oneLine(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', '\v', '\f', '\u0085', '\u2028', '\u2029':
			return ' '
		}
		return r
	}, s)
}

// attachmentBody collects the lines of one attachment.
type attachmentBody struct{ sb strings.Builder }

func (b *attachmentBody) line(s string) {
	if s != "" {
		b.sb.WriteString(attachmentIndent + oneLine(s) + "\n")
	}
}

// text writes a multi-line value, keeping its breaks but indenting them.
func (b *attachmentBody) text(s string) {
	if strings.TrimSpace(s) != "" {
		writeIndented(&b.sb, s, attachmentIndent)
	}
}

func withLink(s, link string) string {
	if s == "" {
		return ""
	}
	if link != "" {
		return s + " <" + link + ">"
	}
	return s
}

func (b *attachmentBody) fields(fields []*model.MessageAttachmentField, truncate int) {
	for _, f := range fields {
		if f == nil {
			continue
		}
		val := ""
		if f.Value != nil {
			val = shorten(fmt.Sprint(f.Value), truncate)
		}
		if f.Title == "" {
			// A leading break would leave an empty first line and hang the
			// rest off the previous field.
			val = strings.TrimLeft(val, "\n\r")
			if val == "" {
				continue
			}
		}
		lines := strings.Split(val, "\n")
		if f.Title == "" {
			b.line(lines[0])
		} else {
			b.line(strings.TrimRight(oneLine(f.Title)+": "+lines[0], " "))
		}
		for _, l := range lines[1:] {
			b.sb.WriteString(continuationIndent + oneLine(l) + "\n")
		}
	}
}

func (b *attachmentBody) actions(actions []*model.PostAction) {
	for _, act := range actions {
		if act == nil {
			continue
		}
		if act.Type != model.PostActionTypeSelect {
			b.line("[button] " + act.Name)
			continue
		}
		var opts []string
		for _, o := range act.Options {
			if o != nil && o.Text != "" {
				opts = append(opts, oneLine(o.Text))
			}
		}
		s := "[select] " + act.Name
		if len(opts) > 0 {
			s += ": " + strings.Join(opts, ", ")
		}
		b.line(s)
	}
}

func writeAttachment(b *strings.Builder, a *model.MessageAttachment, truncate int) {
	var body attachmentBody
	body.text(shorten(a.Pretext, truncate))
	body.line(withLink(a.AuthorName, a.AuthorLink))
	body.line(withLink(a.Title, a.TitleLink))
	body.text(shorten(a.Text, truncate))
	body.fields(a.Fields, truncate)
	if a.ImageURL != "" {
		body.line("image: " + a.ImageURL)
	}
	if a.ThumbURL != "" {
		body.line("thumb: " + a.ThumbURL)
	}
	body.actions(a.Actions)
	body.line(a.Footer)
	if body.sb.Len() == 0 {
		if strings.TrimSpace(a.Fallback) == "" {
			return
		}
		body.text(a.Fallback)
	}
	b.WriteString("  [attachment")
	if a.Color != "" {
		b.WriteString(" " + oneLine(a.Color))
	}
	b.WriteString("]\n")
	b.WriteString(body.sb.String())
}

func writeEmbed(b *strings.Builder, e Embed, names map[string]string) {
	data := asMap(e.Data)
	switch e.Type {
	case string(model.PostEmbedOpengraph):
		s := oneLine(anyString(data, "title"))
		if site := oneLine(anyString(data, "site_name")); site != "" {
			if s != "" {
				s += " — "
			}
			s += site
		}
		if u := oneLine(cmp.Or(anyString(data, "url"), e.URL)); u != "" {
			if s != "" {
				s += ": "
			}
			s += u
		}
		if s != "" {
			b.WriteString("  [link] " + s + "\n")
		}
	case string(model.PostEmbedImage):
		if e.URL != "" {
			b.WriteString("  [image] " + oneLine(e.URL) + "\n")
		}
	case string(model.PostEmbedLink):
		if e.URL != "" {
			b.WriteString("  [link] " + oneLine(e.URL) + "\n")
		}
	case string(model.PostEmbedPermalink):
		id, msg := previewOf(e)
		if id == "" && msg == "" {
			return
		}
		who := ""
		if id != "" {
			who = " @" + oneLine(cmp.Or(names[id], id))
		}
		fmt.Fprintf(b, "  [quote%s] %s\n", who, Snippet(msg, quoteSnippetLength))
	}
}

// writeAcknowledgements writes the line of users who acknowledged a post.
func writeAcknowledgements(b *strings.Builder, acks []Acknowledgement) {
	if len(acks) == 0 {
		return
	}
	users := make([]string, len(acks))
	for i, a := range acks {
		users[i] = "@" + cmp.Or(a.User, a.UserID)
	}
	b.WriteString("  acknowledged: " + strings.Join(users, ", ") + "\n")
}
