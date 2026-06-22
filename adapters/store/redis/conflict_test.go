package redis

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/skosovsky/contexty"
)

func TestIsRedisConflict(t *testing.T) {
	// Arrange.
	t.Parallel()

	assert.False(t, isRedisConflict(nil))
	assert.True(t, isRedisConflict(errors.New("CONFLICT")))
	assert.True(t, isRedisConflict(errors.New("ERR CONFLICT")))
	assert.False(t, isRedisConflict(errors.New("WRONGTYPE")))

	err := evalConflictMap(errors.New("ERR CONFLICT"))
	// Act / Assert: exercise the contract and check its result.
	assert.ErrorIs(t, err, contexty.ErrConversationVersionConflict)
}

func evalConflictMap(err error) error {
	if isRedisConflict(err) {
		return contexty.ErrConversationVersionConflict
	}
	return err
}
