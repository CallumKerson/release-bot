package version

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSemverNext(t *testing.T) {
	tests := []struct {
		current string
		bump    Bump
		want    string
	}{
		{"1.2.3", Patch, "1.2.4"},
		{"1.2.3", Minor, "1.3.0"},
		{"1.2.3", Major, "2.0.0"},
		{"1.2.3", None, "1.2.3"},
		{"0.4.2", Major, "0.5.0"},
		{"0.4.2", Minor, "0.5.0"},
		{"0.4.2", Patch, "0.4.3"},
		{"0.0.1", Major, "0.1.0"},
	}
	scheme, err := NewSemver("")
	require.NoError(t, err)
	for _, test := range tests {
		t.Run(test.current+"/"+test.bump.String(), func(t *testing.T) {
			got, err := scheme.Next(test.current, test.bump, time.Time{})
			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}

func TestSemverInitial(t *testing.T) {
	scheme, err := NewSemver("")
	require.NoError(t, err)
	got, err := scheme.Initial(time.Time{})
	require.NoError(t, err)
	assert.Equal(t, "0.1.0", got)

	scheme, err = NewSemver("1.0.0")
	require.NoError(t, err)
	got, err = scheme.Initial(time.Time{})
	require.NoError(t, err)
	assert.Equal(t, "1.0.0", got)

	_, err = NewSemver("v1")
	require.ErrorIs(t, err, ErrInvalid)
}

func TestSemverValidate(t *testing.T) {
	scheme, err := NewSemver("")
	require.NoError(t, err)
	for _, v := range []string{"0.0.0", "1.2.3", "10.20.30"} {
		assert.NoError(t, scheme.Validate(v), v)
	}
	for _, v := range []string{"", "1.2", "v1.2.3", "01.2.3", "1.2.3-rc.1", "2026.10.0.1"} {
		assert.ErrorIs(t, scheme.Validate(v), ErrInvalid, v)
	}
}

func TestCalverNext(t *testing.T) {
	oct2 := time.Date(2026, time.October, 2, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		format  string
		current string
		now     time.Time
		want    string
	}{
		{"same month increments micro", "YYYY.0M.MICRO", "2026.10.0", oct2, "2026.10.1"},
		{"new month resets micro", "YYYY.0M.MICRO", "2026.09.4", oct2, "2026.10.0"},
		{"new year resets micro", "YYYY.0M.MICRO", "2025.10.4", oct2, "2026.10.0"},
		{"unpadded month", "YYYY.MM.MICRO", "2026.9.4", oct2, "2026.10.0"},
		{"short year", "YY.0M.0D", "26.10.01", oct2, "26.10.02"},
		{"padded day with micro", "YYYY.0M.0D.MICRO", "2026.10.02.3", oct2, "2026.10.02.4"},
		{"iso week", "YYYY.0W.MICRO", "2026.40.0", oct2, "2026.40.1"},
		{
			"iso week year at year end", "YYYY.0W.MICRO", "2026.52.0",
			time.Date(2026, time.December, 29, 0, 0, 0, 0, time.UTC), "2026.53.0",
		},
		{
			"iso week year rolls over early", "YYYY.0W.MICRO", "2020.53.0",
			time.Date(2021, time.January, 2, 0, 0, 0, 0, time.UTC), "2020.53.1",
		},
		{"literal prefix", "release-YYYY.MM.MICRO", "release-2026.10.7", oct2, "release-2026.10.8"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scheme, err := NewCalver(test.format)
			require.NoError(t, err)
			got, err := scheme.Next(test.current, Minor, test.now)
			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}

func TestCalverWithoutMicroRejectsSameDate(t *testing.T) {
	scheme, err := NewCalver("YYYY.0M.0D")
	require.NoError(t, err)
	_, err = scheme.Next("2026.10.02", Patch, time.Date(2026, time.October, 2, 0, 0, 0, 0, time.UTC))
	require.ErrorIs(t, err, ErrInvalid)
}

func TestCalverInitial(t *testing.T) {
	scheme, err := NewCalver("")
	require.NoError(t, err)
	got, err := scheme.Initial(time.Date(2026, time.March, 9, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	assert.Equal(t, "2026.03.0", got)
}

func TestCalverValidate(t *testing.T) {
	scheme, err := NewCalver("YYYY.0M.MICRO")
	require.NoError(t, err)
	assert.NoError(t, scheme.Validate("2026.10.0"))
	assert.NoError(t, scheme.Validate("2026.10.12"))
	for _, v := range []string{"2026.1.0", "26.10.0", "2026.10", "1.2.3"} {
		assert.ErrorIs(t, scheme.Validate(v), ErrInvalid, v)
	}
}

func TestCalverInvalidFormats(t *testing.T) {
	for _, format := range []string{"MICRO", "v1", "YYYY0M", "YYYY.MM.MM", "YYYYMICRO"} {
		_, err := NewCalver(format)
		assert.ErrorIs(t, err, ErrInvalid, format)
	}
}

func TestNewScheme(t *testing.T) {
	_, err := New("semver", "", "")
	require.NoError(t, err)
	_, err = New("calver", "YYYY.MICRO", "")
	require.NoError(t, err)
	_, err = New("romver", "", "")
	require.ErrorIs(t, err, ErrInvalid)
}

func TestParseBump(t *testing.T) {
	for _, bump := range []Bump{None, Patch, Minor, Major} {
		got, err := ParseBump(bump.String())
		require.NoError(t, err)
		assert.Equal(t, bump, got)
	}
	_, err := ParseBump("huge")
	require.ErrorIs(t, err, ErrInvalid)
}
