package render

import (
	"slices"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

func TestUserIDs(t *testing.T) {
	p := &model.Post{UserId: "alice"}
	if got := UserIDs(p); !slices.Equal(got, []string{"alice"}) {
		t.Fatalf("no metadata: got %v", got)
	}
	p.Metadata = &model.PostMetadata{Reactions: []*model.Reaction{
		{UserId: "bob"}, nil, {UserId: "alice"},
	}}
	want := []string{"alice", "bob", "alice"}
	if got := UserIDs(p); !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
