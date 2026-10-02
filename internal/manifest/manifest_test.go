package manifest

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	versions, err := Parse([]byte(`{"app-b": "2026.10.0", "app-a": "1.2.0"}`))
	require.NoError(t, err)
	assert.Equal(t, Manifest{"app-a": "1.2.0", "app-b": "2026.10.0"}, versions)

	versions, err = Parse([]byte("  \n"))
	require.NoError(t, err)
	assert.Empty(t, versions)

	_, err = Parse([]byte(`{"app-a": 1}`))
	require.ErrorIs(t, err, ErrInvalid)
	_, err = Parse([]byte(`[]`))
	require.ErrorIs(t, err, ErrInvalid)
}

func TestMarshalIsSortedAndStable(t *testing.T) {
	data, err := Manifest{"b": "2.0.0", "a": "1.0.0"}.Marshal()
	require.NoError(t, err)
	assert.Equal(t, "{\n  \"a\": \"1.0.0\",\n  \"b\": \"2.0.0\"\n}\n", string(data))

	data, err = Manifest(nil).Marshal()
	require.NoError(t, err)
	assert.Equal(t, "{}\n", string(data))
}

func TestWithCopies(t *testing.T) {
	original := Manifest{"a": "1.0.0", "b": "1.0.0"}
	updated := original.With(map[string]string{"a": "1.1.0", "c": "0.1.0"})
	assert.Equal(t, Manifest{"a": "1.1.0", "b": "1.0.0", "c": "0.1.0"}, updated)
	assert.Equal(t, Manifest{"a": "1.0.0", "b": "1.0.0"}, original)
}
