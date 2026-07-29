package notify

import "context"

type Channel interface {
	Name() string
	// Send delivers a notification. replyMsgID is the channel-native ID of a
	// prior message this message should thread under (empty = no reply).
	// Returns the sent message's channel-native ID (empty if not available).
	Send(ctx context.Context, recipientID, replyMsgID string, eventType EventType, payload any) (string, error)
}
