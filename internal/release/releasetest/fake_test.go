package releasetest

import (
	"testing"

	"github.com/CallumKerson/release-bot/internal/release"
)

func TestFakeContract(t *testing.T) {
	RunContract(t, func(_ *testing.T, history ...Commit) release.Repo { return NewFake(history...) })
}
