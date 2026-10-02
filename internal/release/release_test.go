package release

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBranchJSONListsPathsNotContents(t *testing.T) {
	branch := &Branch{
		Name:    "release-bot/release",
		Message: "chore(release): app 1.0.0",
		Files:   map[string][]byte{"b/CHANGELOG.md": []byte("b"), "a.json": []byte("a")},
	}
	data, err := json.Marshal(branch)
	require.NoError(t, err)
	assert.JSONEq(
		t,
		`{"name": "release-bot/release", "message": "chore(release): app 1.0.0", "files": ["a.json", "b/CHANGELOG.md"]}`,
		string(data),
	)
}
