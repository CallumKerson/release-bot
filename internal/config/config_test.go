package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CallumKerson/release-bot/internal/version"
)

const monorepo = `
[defaults]
scheme = "semver"

[bump]
deps = "patch"

[packages.app-a]
path = "apps/app-a/"
depends-on = ["lib-1", "lib-2"]

[packages.app-b]
path = "./apps/app-b"
depends-on = ["lib-1"]
scheme = "calver"
calver-format = "YYYY.0M.MICRO"

[packages.lib-1]
path = "libs/lib-1"
release = false

[packages.lib-2]
path = "libs/lib-2"
release = false
also = ["proto/**"]
exclude = ["libs/lib-2/docs/**"]
`

func TestParseMonorepo(t *testing.T) {
	cfg, err := Parse([]byte(monorepo))
	require.NoError(t, err)

	assert.Equal(t, DefaultBranch, cfg.Branch)
	assert.Equal(t, DefaultManifest, cfg.Manifest)
	assert.Equal(t, map[string]version.Bump{"deps": version.Patch}, cfg.Bumps)

	names := make([]string, 0, len(cfg.Packages))
	for i := range cfg.Packages {
		names = append(names, cfg.Packages[i].Name)
	}
	assert.Equal(t, []string{"app-a", "app-b", "lib-1", "lib-2"}, names)

	appA := cfg.Package("app-a")
	require.NotNil(t, appA)
	assert.Equal(t, &Package{
		Name: "app-a", Path: "apps/app-a", Release: true, Scheme: "semver",
		Tag: "{name}-v{version}", Changelog: "apps/app-a/CHANGELOG.md", DependsOn: []string{"lib-1", "lib-2"},
	}, appA)
	assert.Equal(t, "app-a-v1.2.3", appA.TagFor("1.2.3"))

	appB := cfg.Package("app-b")
	assert.Equal(t, "apps/app-b", appB.Path)
	assert.Equal(t, "calver", appB.Scheme)
	assert.Equal(t, "YYYY.0M.MICRO", appB.CalverFormat)

	lib2 := cfg.Package("lib-2")
	assert.False(t, lib2.Release)

	released := cfg.Released()
	require.Len(t, released, 2)
	assert.Equal(t, "app-a", released[0].Name)
	assert.Equal(t, "app-b", released[1].Name)
}

func TestParseRootPackage(t *testing.T) {
	cfg, err := Parse([]byte(`
branch = "releases/next"
manifest = ".config/versions.json"

[packages.tool]
path = "."
initial-version = "1.0.0"
`))
	require.NoError(t, err)
	assert.Equal(t, "releases/next", cfg.Branch)
	assert.Equal(t, ".config/versions.json", cfg.Manifest)
	tool := cfg.Package("tool")
	assert.Equal(t, "v1.0.0", tool.TagFor("1.0.0"))
	assert.Equal(t, "CHANGELOG.md", tool.Changelog)
	assert.Equal(t, "1.0.0", tool.InitialVersion)
}

func TestParseDefaultsApplyToEveryPackage(t *testing.T) {
	cfg, err := Parse([]byte(`
[defaults]
scheme = "calver"
calver-format = "YY.0M.MICRO"
tag = "{path}/v{version}"

[packages.root]
path = "."

[packages.svc]
path = "svc"
tag = "svc@{version}"
`))
	require.NoError(t, err)
	root := cfg.Package("root")
	assert.Equal(t, "v26.10.0", root.TagFor("26.10.0"))
	assert.Equal(t, "YY.0M.MICRO", root.CalverFormat)
	svc := cfg.Package("svc")
	assert.Equal(t, "svc@26.10.0", svc.TagFor("26.10.0"))
	assert.Equal(t, "calver", svc.Scheme)
}

func TestParseErrors(t *testing.T) {
	tests := map[string]string{
		"unknown key":          "[packages.a]\npath = \"a\"\nversion-file = \"x\"",
		"no packages":          `branch = "x"`,
		"nothing released":     "[packages.a]\npath = \"a\"\nrelease = false",
		"missing path":         "[packages.a]\nscheme = \"semver\"",
		"path escapes repo":    "[packages.a]\npath = \"../a\"",
		"absolute path":        "[packages.a]\npath = \"/a\"",
		"duplicate path":       "[packages.a]\npath = \"x\"\n[packages.b]\npath = \"./x/\"",
		"unknown scheme":       "[packages.a]\npath = \"a\"\nscheme = \"romver\"",
		"bad calver format":    "[packages.a]\npath = \"a\"\nscheme = \"calver\"\ncalver-format = \"MICRO\"",
		"bad initial version":  "[packages.a]\npath = \"a\"\ninitial-version = \"one\"",
		"tag without version":  "[packages.a]\npath = \"a\"\ntag = \"latest\"",
		"tag not a ref":        "[packages.a]\npath = \"a\"\ntag = \"a v{version}\"",
		"tag starts with dot":  "[packages.a]\npath = \"a\"\ntag = \".{name}-{version}\"",
		"duplicate tag":        "[defaults]\ntag = \"v{version}\"\n[packages.a]\npath = \"a\"\n[packages.b]\npath = \"b\"",
		"duplicate changelog":  "[packages.a]\npath = \"a\"\n[packages.b]\npath = \"b\"\nchangelog = \"a/CHANGELOG.md\"",
		"unknown dependency":   "[packages.a]\npath = \"a\"\ndepends-on = [\"nope\"]",
		"self dependency":      "[packages.a]\npath = \"a\"\ndepends-on = [\"a\"]",
		"bad glob":             "[packages.a]\npath = \"a\"\nalso = [\"[\"]",
		"bad bump":             "[bump]\ndocs = \"huge\"\n[packages.a]\npath = \"a\"",
		"bad branch":           "branch = \"release..next\"\n[packages.a]\npath = \"a\"",
		"bad package name":     "[packages.\"a b\"]\npath = \"a\"",
		"dependency cycle":     "[packages.a]\npath = \"a\"\ndepends-on = [\"b\"]\n[packages.b]\npath = \"b\"\ndepends-on = [\"c\"]\n[packages.c]\npath = \"c\"\ndepends-on = [\"a\"]",
		"malformed toml":       "[packages.a\npath = \"a\"",
		"wrong type for paths": "[packages.a]\npath = \"a\"\nalso = \"x\"",
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(input))
			require.ErrorIs(t, err, ErrInvalid)
		})
	}
}

func TestContainsExcludesAndAlso(t *testing.T) {
	cfg, err := Parse([]byte(monorepo))
	require.NoError(t, err)
	lib2 := cfg.Package("lib-2")

	assert.True(t, lib2.Contains("libs/lib-2/x.go"))
	assert.True(t, lib2.Contains("libs/lib-2/docs/readme.md"))
	assert.False(t, lib2.Contains("libs/lib-20/x.go"))
	assert.False(t, lib2.Excludes("libs/lib-2/x.go"))
	assert.True(t, lib2.Excludes("libs/lib-2/docs/readme.md"))

	glob, ok := lib2.AlsoMatch("proto/v1/a.proto")
	assert.True(t, ok)
	assert.Equal(t, "proto/**", glob)
	_, ok = lib2.AlsoMatch("protobuf/a.proto")
	assert.False(t, ok)

	root := &Package{Path: "."}
	assert.True(t, root.Contains("anything/at/all"))
}

func TestFind(t *testing.T) {
	dir := t.TempDir()
	_, err := Find(dir, "")
	require.Error(t, err)

	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".config"), 0o750))
	nested := filepath.Join(dir, ".config", "release-bot.toml")
	require.NoError(t, os.WriteFile(nested, []byte(""), 0o600))
	found, err := Find(dir, "")
	require.NoError(t, err)
	assert.Equal(t, nested, found)

	root := filepath.Join(dir, "release-bot.toml")
	require.NoError(t, os.WriteFile(root, []byte(""), 0o600))
	found, err = Find(dir, "")
	require.NoError(t, err)
	assert.Equal(t, root, found)

	found, err = Find(dir, "custom.toml")
	require.NoError(t, err)
	assert.Equal(t, "custom.toml", found)
}

func TestLoad(t *testing.T) {
	file := filepath.Join(t.TempDir(), "release-bot.toml")
	require.NoError(t, os.WriteFile(file, []byte(monorepo), 0o600))
	cfg, err := Load(file)
	require.NoError(t, err)
	assert.Len(t, cfg.Packages, 4)

	_, err = Load(filepath.Join(t.TempDir(), "missing.toml"))
	require.Error(t, err)
}
