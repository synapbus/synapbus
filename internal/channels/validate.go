package channels

import (
	"fmt"
	"regexp"
	"strings"
)

var channelNameRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

// ValidateChannelName validates a channel name.
// Names must be alphanumeric plus hyphens and underscores, max 64 characters.
// Names are normalized to lowercase.
func ValidateChannelName(name string) error {
	if name == "" {
		return ErrInvalidChannelName
	}
	if len(name) > 64 {
		return fmt.Errorf("%w: name exceeds 64 characters", ErrInvalidChannelName)
	}
	if !channelNameRegex.MatchString(name) {
		return fmt.Errorf("%w: name must be alphanumeric with hyphens and underscores", ErrInvalidChannelName)
	}
	return nil
}

// NormalizeChannelName returns the lowercase form of a channel name.
func NormalizeChannelName(name string) string {
	return strings.ToLower(name)
}
