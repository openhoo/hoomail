package events

import "sync"

type Type string

const (
	TypeMailboxNew      Type = "mailbox:new"
	TypeMailboxDeleted  Type = "mailbox:deleted"
	TypeMessagesChanged Type = "messages:changed"
	TypeCalendarChanged Type = "calendar:changed"
	TypeMessageNew      Type = "message:new"
	TypeReset           Type = "reset"
)

type Mailbox struct {
	ID      int64  `json:"id"`
	Address string `json:"address"`
}

type MessageSummary struct {
	ID          int64   `json:"id"`
	Subject     *string `json:"subject"`
	FromAddress *string `json:"fromAddress"`
	FromName    *string `json:"fromName"`
}

type Event struct {
	Type      Type            `json:"type"`
	Mailbox   *Mailbox        `json:"mailbox,omitempty"`
	MailboxID *int64          `json:"mailboxId,omitempty"`
	Message   *MessageSummary `json:"message,omitempty"`
}

func MailboxNew(id int64, address string) Event {
	return Event{Type: TypeMailboxNew, Mailbox: &Mailbox{ID: id, Address: address}}
}

func MailboxDeleted(mailboxID int64) Event {
	return mailboxEvent(TypeMailboxDeleted, mailboxID)
}

func MessagesChanged(mailboxID int64) Event {
	return mailboxEvent(TypeMessagesChanged, mailboxID)
}

func CalendarChanged(mailboxID int64) Event {
	return mailboxEvent(TypeCalendarChanged, mailboxID)
}

func MessageNew(mailboxID, messageID int64, subject, fromAddress, fromName *string) Event {
	return Event{
		Type:      TypeMessageNew,
		MailboxID: int64Pointer(mailboxID),
		Message: &MessageSummary{
			ID:          messageID,
			Subject:     subject,
			FromAddress: fromAddress,
			FromName:    fromName,
		},
	}
}

func Reset() Event {
	return Event{Type: TypeReset}
}

func mailboxEvent(eventType Type, mailboxID int64) Event {
	return Event{Type: eventType, MailboxID: int64Pointer(mailboxID)}
}

func int64Pointer(value int64) *int64 {
	return &value
}

const subscriberBuffer = 64

type Hub struct {
	mu          sync.Mutex
	nextID      uint64
	subscribers map[uint64]chan Event
}

func NewHub() *Hub {
	return &Hub{subscribers: make(map[uint64]chan Event)}
}

func (hub *Hub) Subscribe() (<-chan Event, func()) {
	channel := make(chan Event, subscriberBuffer)

	hub.mu.Lock()
	id := hub.nextID
	hub.nextID++
	hub.subscribers[id] = channel
	hub.mu.Unlock()

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			hub.mu.Lock()
			if current, exists := hub.subscribers[id]; exists && current == channel {
				delete(hub.subscribers, id)
				close(channel)
			}
			hub.mu.Unlock()
		})
	}
	return channel, unsubscribe
}

func (hub *Hub) Broadcast(event Event) {
	hub.mu.Lock()
	for id, channel := range hub.subscribers {
		select {
		case channel <- event:
		default:
			delete(hub.subscribers, id)
			close(channel)
		}
	}
	hub.mu.Unlock()
}

var global = NewHub()

func Subscribe() (<-chan Event, func()) {
	return global.Subscribe()
}

func Broadcast(event Event) {
	global.Broadcast(event)
}
