package events

import (
	"encoding/json"
	"testing"
	"time"
)

func TestEventJSONMatchesLegacyPayloads(t *testing.T) {
	subject := "Welcome"
	fromAddress := "sender@example.com"

	tests := []struct {
		name  string
		event Event
		want  string
	}{
		{"mailbox new", MailboxNew(7, "inbox@example.com"), `{"type":"mailbox:new","mailbox":{"id":7,"address":"inbox@example.com"}}`},
		{"mailbox deleted", MailboxDeleted(7), `{"type":"mailbox:deleted","mailboxId":7}`},
		{"messages changed", MessagesChanged(7), `{"type":"messages:changed","mailboxId":7}`},
		{"calendar changed", CalendarChanged(7), `{"type":"calendar:changed","mailboxId":7}`},
		{"message new", MessageNew(7, 11, &subject, &fromAddress, nil), `{"type":"message:new","mailboxId":7,"message":{"id":11,"subject":"Welcome","fromAddress":"sender@example.com","fromName":null}}`},
		{"reset", Reset(), `{"type":"reset"}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload, err := json.Marshal(test.event)
			if err != nil {
				t.Fatalf("marshal event: %v", err)
			}
			if string(payload) != test.want {
				t.Fatalf("payload = %s, want %s", payload, test.want)
			}
		})
	}
}

func TestHubBroadcastFansOutInOrder(t *testing.T) {
	hub := NewHub()
	first, unsubscribeFirst := hub.Subscribe()
	second, unsubscribeSecond := hub.Subscribe()
	defer unsubscribeFirst()
	defer unsubscribeSecond()

	want := []Event{MailboxNew(1, "first@example.com"), MessagesChanged(1), Reset()}
	for _, event := range want {
		hub.Broadcast(event)
	}

	assertEvents(t, first, want)
	assertEvents(t, second, want)
}

func TestSubscriptionCloseUnsubscribesAndClosesChannel(t *testing.T) {
	hub := NewHub()
	closed, unsubscribeClosed := hub.Subscribe()
	active, unsubscribeActive := hub.Subscribe()
	defer unsubscribeActive()

	unsubscribeClosed()
	unsubscribeClosed()

	if _, open := <-closed; open {
		t.Fatal("closed subscription channel remains open")
	}

	want := MailboxDeleted(23)
	hub.Broadcast(want)
	assertEvents(t, active, []Event{want})
}

func TestSlowSubscriberDoesNotBlockBroadcast(t *testing.T) {
	hub := NewHub()
	slow, unsubscribeSlow := hub.Subscribe()
	active, unsubscribeActive := hub.Subscribe()
	defer unsubscribeSlow()
	defer unsubscribeActive()

	for index := range subscriberBuffer {
		hub.Broadcast(MessagesChanged(int64(index)))
		<-active
	}

	done := make(chan struct{})
	go func() {
		hub.Broadcast(Reset())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("broadcast blocked on slow subscriber")
	}

	assertEvents(t, active, []Event{Reset()})
	for range slow {
	}
}

func assertEvents(t *testing.T, channel <-chan Event, want []Event) {
	t.Helper()
	for index, expected := range want {
		select {
		case actual, open := <-channel:
			if !open {
				t.Fatalf("event %d: channel closed", index)
			}
			actualJSON, err := json.Marshal(actual)
			if err != nil {
				t.Fatalf("marshal actual event: %v", err)
			}
			expectedJSON, err := json.Marshal(expected)
			if err != nil {
				t.Fatalf("marshal expected event: %v", err)
			}
			if string(actualJSON) != string(expectedJSON) {
				t.Fatalf("event %d = %s, want %s", index, actualJSON, expectedJSON)
			}
		case <-time.After(time.Second):
			t.Fatalf("event %d not received", index)
		}
	}
}
