package releasetest

import (
	"testing"

	"github.com/CallumKerson/release-bot/internal/release"
)

func TestFakeContract(t *testing.T) {
	RunContract(t, func(_ *testing.T, history ...Commit) release.Repo { return NewFake(history...) })
}

func TestFakeRemoteContract(t *testing.T) {
	RunRemoteContract(
		t,
		func(_ *testing.T, history ...Commit) (release.Repo, release.Remote, func(string) (string, bool)) {
			fake := NewFake(history...)
			return fake, fake, fake.Pushed
		},
	)
}
