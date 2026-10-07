package contexty

import (
	"errors"
	"fmt"
)

var ErrInvalidRole = errors.New("contexty: invalid message role")

// Validate checks the closed semantic role set. Roles do not grant trust or permissions.
func (r Role) Validate() error {
	switch r {
	case RoleSystem, RoleDeveloper, RoleUser, RoleAssistant, RoleTool:
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrInvalidRole, r)
	}
}

func validateMessageRoles(messages []Message) error {
	for _, message := range messages {
		if err := message.Role.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func validateSnapshotRoles(snapshot ConversationSnapshot) error {
	for _, messages := range snapshot.segments {
		if err := validateMessageRoles(messages); err != nil {
			return err
		}
	}
	return nil
}
