package channels

import "errors"

// Sentinel errors for channel operations.
var (
	ErrChannelNotFound    = errors.New("channel not found")
	ErrChannelNameConflict = errors.New("channel name already exists")
	ErrNotChannelMember   = errors.New("not a channel member")
	ErrNotChannelOwner    = errors.New("not the channel owner")
	ErrOwnerCannotLeave   = errors.New("channel owner cannot leave; transfer ownership or delete the channel first")
	ErrNotInvited         = errors.New("not invited to this private channel")
	ErrInvalidChannelName = errors.New("invalid channel name")
)
