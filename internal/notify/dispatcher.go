package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Dispatcher interface {
	Dispatch(ctx context.Context, eventType EventType, payload any) error
}

type subscriberTarget struct {
	subscriberID string
	channel      string
	recipientID  string
}

type DBDispatcher struct {
	db       *pgxpool.Pool
	channels map[string]Channel

	mu         sync.Mutex
	openMsgIDs map[string]map[string]string
}

func NewDispatcher(db *pgxpool.Pool) *DBDispatcher {
	return &DBDispatcher{
		db:         db,
		channels:   make(map[string]Channel),
		openMsgIDs: make(map[string]map[string]string),
	}
}

func (d *DBDispatcher) Register(ch Channel) {
	d.channels[ch.Name()] = ch
}

func (d *DBDispatcher) Dispatch(ctx context.Context, eventType EventType, payload any) error {
	targets, err := d.loadTargets(ctx)
	if err != nil {
		return fmt.Errorf("notify: load targets: %w", err)
	}

	payloadJSON, _ := json.Marshal(payload)

	var positionID string
	var replyMap map[string]string
	switch eventType {
	case EventTradeOpened:
		if p, ok := payload.(TradeOpenedPayload); ok {
			positionID = p.PositionID
		}
	case EventTradeClosed:
		if p, ok := payload.(TradeClosedPayload); ok {
			positionID = p.PositionID
		}
		if positionID != "" {
			d.mu.Lock()
			replyMap = d.openMsgIDs[positionID]
			delete(d.openMsgIDs, positionID)
			d.mu.Unlock()
		}
	}

	for _, t := range targets {
		ch, ok := d.channels[t.channel]
		if !ok {
			continue
		}
		replyMsgID := ""
		if replyMap != nil {
			replyMsgID = replyMap[t.recipientID]
		}
		msgID, sendErr := ch.Send(ctx, t.recipientID, replyMsgID, eventType, payload)
		status := "sent"
		var errText *string
		if sendErr != nil {
			slog.Warn("notify: send failed", "channel", t.channel, "recipient", t.recipientID, "err", sendErr)
			status = "failed"
			s := sendErr.Error()
			errText = &s
		} else if eventType == EventTradeOpened && positionID != "" && msgID != "" {
			d.mu.Lock()
			if d.openMsgIDs[positionID] == nil {
				d.openMsgIDs[positionID] = make(map[string]string)
			}
			d.openMsgIDs[positionID][t.recipientID] = msgID
			d.mu.Unlock()
		}
		d.log(ctx, t.subscriberID, t.channel, eventType, payloadJSON, status, errText)
	}
	return nil
}

func (d *DBDispatcher) loadTargets(ctx context.Context) ([]subscriberTarget, error) {
	const q = `
		SELECT st.subscriber_id, st.channel, st.recipient_id
		FROM subscriber_targets st
		JOIN subscribers s ON s.id = st.subscriber_id
		WHERE s.status = 'active' AND st.status = 'active'`
	rows, err := d.db.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var targets []subscriberTarget
	for rows.Next() {
		var t subscriberTarget
		if err := rows.Scan(&t.subscriberID, &t.channel, &t.recipientID); err != nil {
			return nil, err
		}
		targets = append(targets, t)
	}
	return targets, rows.Err()
}

func (d *DBDispatcher) log(ctx context.Context, subscriberID, channel string, eventType EventType, payload []byte, status string, errText *string) {
	const q = `
		INSERT INTO notification_log (subscriber_id, channel, type, payload, status, error, sent_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`
	if _, err := d.db.Exec(ctx, q, subscriberID, channel, string(eventType), payload, status, errText, time.Now()); err != nil {
		slog.Warn("notify: log insert failed", "err", err)
	}
}
