package releasetest

import (
	"testing"

	"github.com/CallumKerson/release-bot/internal/release"
)

func TestFakeContract(t *testing.T) {
	RunContract(t, func(_ *testing.T, history ...Commit) release.Repo { return NewFake(history...) })
}

func TestFakeHostContract(t *testing.T) {
	RunHostContract(t, func(_ *testing.T, history ...Commit) *HostFixture {
		fake := NewFake(history...)
		host := NewFakeHost(fake)
		return &HostFixture{
			Repo: fake, Host: host,
			Merge: func(_ *testing.T, number int) { host.Merge(number) },
		}
	})
}
