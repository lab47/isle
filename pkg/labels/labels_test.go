package labels

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLabels(t *testing.T) {
	t.Run("matches false properly", func(t *testing.T) {
		a := New("component", "shell")
		b := New("component", "vm")

		require.False(t, a.Match(b))
	})

	t.Run("matches true properly", func(t *testing.T) {
		a := New("component", "shell")
		b := New("component", "shell")

		require.True(t, a.Match(b))
	})

	t.Run("skips missing properly", func(t *testing.T) {
		a := New("a", "x", "component", "shell", "d", "blah")
		b := New("component", "shell")

		require.True(t, a.Match(b))
	})

	t.Run("reqires all keys to be present", func(t *testing.T) {
		a := New("a", "x", "component", "shell")
		b := New("b", "y", "component", "shell")

		require.False(t, a.Match(b))

		a = New("b", "x", "component", "shell")
		b = New("a", "y", "component", "shell")

		require.False(t, a.Match(b))
	})
}
