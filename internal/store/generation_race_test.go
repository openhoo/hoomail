package store

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openhoo/hoomail/internal/events"
)

type generationEventLog struct {
	mu     sync.Mutex
	events []events.Event
}

func (log *generationEventLog) record(event events.Event) {
	log.mu.Lock()
	defer log.mu.Unlock()
	log.events = append(log.events, event)
}

func (log *generationEventLog) snapshot() []events.Event {
	log.mu.Lock()
	defer log.mu.Unlock()
	return append([]events.Event(nil), log.events...)
}

// Mutation callbacks run after the serialization lock is released. Concurrent
// commits may therefore publish overlapping invalidations; the stream is not
// an ordered database history. Validate its payloads and check authoritative
// post-race state separately rather than inferring generations from event order.
func validateConcurrentEventPayloads(t *testing.T, stream []events.Event) {
	t.Helper()
	for _, event := range stream {
		switch event.Type {
		case events.TypeMailboxNew:
			if event.Mailbox == nil || event.Mailbox.ID <= 0 || event.Mailbox.Address == "" || event.MailboxID != nil || event.Message != nil {
				t.Fatalf("invalid mailbox:new payload: %+v", event)
			}
		case events.TypeReset:
			if event.Mailbox != nil || event.MailboxID != nil || event.Message != nil {
				t.Fatalf("invalid reset payload: %+v", event)
			}
		case events.TypeMessageNew:
			if event.MailboxID == nil || *event.MailboxID <= 0 || event.Message == nil || event.Message.ID <= 0 || event.Mailbox != nil {
				t.Fatalf("invalid message:new payload: %+v", event)
			}
		case events.TypeMessagesChanged, events.TypeCalendarChanged, events.TypeMailboxDeleted:
			if event.MailboxID == nil || *event.MailboxID <= 0 || event.Mailbox != nil || event.Message != nil {
				t.Fatalf("invalid mailbox invalidation payload: %+v", event)
			}
		default:
			t.Fatalf("unknown event type: %q", event.Type)
		}
	}
}

func validateAuthoritativeGenerationState(t *testing.T, store *Store, address, marker string, requireMailbox, requireMessage bool) {
	t.Helper()
	ctx := context.Background()
	mailboxes, err := store.ListMailboxes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(mailboxes) > 1 || requireMailbox && len(mailboxes) != 1 {
		t.Fatalf("post-race mailboxes = %+v", mailboxes)
	}
	messageCount := 0
	for _, mailbox := range mailboxes {
		if mailbox.Address != address {
			t.Fatalf("post-race mailbox belongs to old generation: %+v", mailbox)
		}
		messages, err := store.ListMessages(ctx, mailbox.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(messages) > 1 || requireMessage && len(messages) != 1 || mailbox.TotalCount != int64(len(messages)) {
			t.Fatalf("post-race mailbox count mismatch: mailbox=%+v messages=%+v", mailbox, messages)
		}
		var unread int64
		for _, message := range messages {
			if message.IsRead == 0 {
				unread++
			}
			detail, err := store.GetMessage(ctx, message.ID)
			if err != nil || detail == nil {
				t.Fatalf("post-race detail missing: detail=%+v error=%v", detail, err)
			}
			if detail.Message.MailboxID != mailbox.ID || detail.Message.Subject == nil || *detail.Message.Subject != marker+"-subject" || len(detail.Attachments) != 1 || detail.Attachments[0].Filename == nil || *detail.Attachments[0].Filename != marker+".att" {
				t.Fatalf("post-race detail contains old generation: %+v", detail)
			}
			raw, found, err := store.GetMessageSource(ctx, message.ID)
			if err != nil || !found || string(raw) != "raw-"+marker {
				t.Fatalf("post-race source = %q, found=%t error=%v", raw, found, err)
			}
		}
		if mailbox.UnreadCount != unread {
			t.Fatalf("post-race unread count = %d, want %d", mailbox.UnreadCount, unread)
		}
		messageCount += len(messages)
	}
	var persistedMessages, persistedAttachments int
	if err := store.DB().QueryRow(`SELECT (SELECT COUNT(*) FROM messages),(SELECT COUNT(*) FROM attachments)`).Scan(&persistedMessages, &persistedAttachments); err != nil {
		t.Fatal(err)
	}
	if persistedMessages != messageCount || persistedAttachments != messageCount {
		t.Fatalf("post-race orphaned or mixed rows: messages=%d attachments=%d, want %d each", persistedMessages, persistedAttachments, messageCount)
	}
}

func generationMessageInput(address, marker string) StoreMessageInput {
	subject := marker + "-subject"
	filename := marker + ".att"
	contentType := "application/octet-stream"
	return StoreMessageInput{
		Recipients:  []string{address},
		Subject:     &subject,
		Headers:     map[string]string{},
		Raw:         []byte("raw-" + marker),
		Attachments: []AttachmentInput{{Filename: &filename, ContentType: &contentType, Content: []byte(marker)}},
	}
}

func TestGetMessageDetailStaysWithinSingleGeneration(t *testing.T) {
	log := &generationEventLog{}
	store := openTestStore(t, WithBroadcaster(log.record))
	ctx := context.Background()
	violations := make(chan string, 16)
	stop := make(chan struct{})
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			select {
			case <-stop:
				return
			default:
			}
			detail, err := store.GetMessage(ctx, 1)
			if err != nil {
				violations <- fmt.Sprintf("GetMessage: %v", err)
				return
			}
			if detail == nil {
				continue
			}
			subject := ""
			if detail.Message.Subject != nil {
				subject = *detail.Message.Subject
			}
			expectedFilename := strings.TrimSuffix(subject, "-subject") + ".att"
			if len(detail.Attachments) != 1 || detail.Attachments[0].Filename == nil || *detail.Attachments[0].Filename != expectedFilename {
				select {
				case violations <- fmt.Sprintf("mixed-generation detail: subject=%q attachments=%+v", subject, detail.Attachments):
				default:
				}
			}
		}
	}()

	const rounds = 120
	for round := 0; round < rounds; round++ {
		if _, err := store.StoreMessage(ctx, generationMessageInput("churn@example.com", "alpha")); err != nil {
			t.Fatalf("deliver alpha in round %d: %v", round, err)
		}
		start := make(chan struct{})
		errs := make(chan error, 2)
		var wait sync.WaitGroup
		wait.Add(2)
		go func() {
			defer wait.Done()
			<-start
			errs <- store.WipeAll(ctx)
		}()
		go func() {
			defer wait.Done()
			<-start
			_, err := store.StoreMessage(ctx, generationMessageInput("churn@example.com", "beta"))
			errs <- err
		}()
		close(start)
		wait.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("generation churn in round %d: %v", round, err)
			}
		}
	}
	close(stop)
	<-readerDone
	select {
	case violation := <-violations:
		t.Fatal(violation)
	default:
	}
	validateConcurrentEventPayloads(t, log.snapshot())
	validateAuthoritativeGenerationState(t, store, "churn@example.com", "beta", false, false)
}

func TestBulkMutationsSerializeAgainstWipeAll(t *testing.T) {
	tests := []struct {
		name  string
		apply func(context.Context, *Store, []int64) ([]int64, error)
	}{
		{name: "mark-read", apply: func(ctx context.Context, store *Store, ids []int64) ([]int64, error) {
			return store.SetReadState(ctx, ids, true)
		}},
		{name: "mark-unread", apply: func(ctx context.Context, store *Store, ids []int64) ([]int64, error) {
			return store.SetReadState(ctx, ids, false)
		}},
		{name: "delete", apply: func(ctx context.Context, store *Store, ids []int64) ([]int64, error) {
			return store.DeleteMessages(ctx, ids)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			store := openTestStore(t)
			stored, err := store.StoreMessage(ctx, generationMessageInput("fence@example.com", "fence"))
			if err != nil {
				t.Fatal(err)
			}
			store.pop3Mu.Lock()
			done := make(chan error, 1)
			go func() {
				_, err := test.apply(ctx, store, []int64{stored[0].MessageID})
				done <- err
			}()
			blocked := false
			select {
			case <-done:
			case <-time.After(75 * time.Millisecond):
				blocked = true
			}
			store.pop3Mu.Unlock()
			if !blocked {
				t.Fatal("bulk mutation bypassed POP3 serialization lock")
			}
			if err := <-done; err != nil {
				t.Fatalf("bulk mutation after lock release: %v", err)
			}

			log := &generationEventLog{}
			store = openTestStore(t, WithBroadcaster(log.record))
			const rounds = 40
			for round := 0; round < rounds; round++ {
				first, err := store.StoreMessage(ctx, generationMessageInput("first@example.com", "first"))
				if err != nil {
					t.Fatalf("first delivery in round %d: %v", round, err)
				}
				second, err := store.StoreMessage(ctx, generationMessageInput("second@example.com", "second"))
				if err != nil {
					t.Fatalf("second delivery in round %d: %v", round, err)
				}
				start := make(chan struct{})
				errs := make(chan error, 2)
				var wait sync.WaitGroup
				wait.Add(2)
				go func() {
					defer wait.Done()
					<-start

					_, err := test.apply(ctx, store, []int64{first[0].MessageID, second[0].MessageID})
					errs <- err
				}()
				go func() {
					defer wait.Done()
					<-start
					if err := store.WipeAll(ctx); err != nil {
						errs <- err
						return
					}
					_, err := store.StoreMessage(ctx, generationMessageInput("replacement@example.com", "replacement"))
					errs <- err
				}()
				close(start)
				wait.Wait()
				close(errs)
				for err := range errs {
					if err != nil {
						t.Fatalf("bulk/reset churn in round %d: %v", round, err)
					}
				}
			}
			validateConcurrentEventPayloads(t, log.snapshot())
			validateAuthoritativeGenerationState(t, store, "replacement@example.com", "replacement", true, false)
		})
	}
}

func TestWipeAllPublishesAfterUnlockingPOP3Serialization(t *testing.T) {
	var streamMu sync.Mutex
	var stream []events.Event
	messagePublished := make(chan struct{})
	resetPublished := make(chan struct{})
	releasePublication := make(chan struct{})
	var messageGate sync.Once
	var resetGate sync.Once
	store := openTestStore(t, WithBroadcaster(func(event events.Event) {
		streamMu.Lock()
		stream = append(stream, event)
		streamMu.Unlock()
		switch event.Type {
		case events.TypeMessageNew:
			messageGate.Do(func() {
				close(messagePublished)
				<-releasePublication
			})
		case events.TypeReset:
			resetGate.Do(func() { close(resetPublished) })
		}
	}))

	storeDone := make(chan error, 1)
	go func() {
		_, err := store.StoreMessage(context.Background(), generationMessageInput("sequenced@example.com", "store"))
		storeDone <- err
	}()
	select {
	case <-messagePublished:
	case <-time.After(time.Second):
		t.Fatal("StoreMessage did not reach event publication")
	}

	wipeDone := make(chan error, 1)
	go func() { wipeDone <- store.WipeAll(context.Background()) }()
	select {
	case <-resetPublished:
	case <-time.After(time.Second):
		t.Fatal("WipeAll did not publish reset after StoreMessage publication blocked")
	}
	select {
	case err := <-wipeDone:
		if err != nil {
			t.Fatalf("WipeAll: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("WipeAll remained blocked by StoreMessage broadcaster")
	}
	var count int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("WipeAll did not commit before returning: message count=%d", count)
	}

	select {
	case err := <-storeDone:
		t.Fatalf("StoreMessage returned before broadcaster release: %v", err)
	default:
	}
	close(releasePublication)
	if err := <-storeDone; err != nil {
		t.Fatalf("StoreMessage: %v", err)
	}
	streamMu.Lock()
	defer streamMu.Unlock()
	if len(stream) != 3 || stream[0].Type != events.TypeMailboxNew || stream[1].Type != events.TypeMessageNew || stream[2].Type != events.TypeReset {
		t.Fatalf("event order=%v", stream)
	}
}
func TestMarkReadDetailIgnoresResetReplacement(t *testing.T) {
	log := &generationEventLog{}
	store := openTestStore(t, WithBroadcaster(log.record))
	ctx := context.Background()
	first, err := store.StoreMessage(ctx, generationMessageInput("before@example.com", "before"))
	if err != nil {
		t.Fatal(err)
	}
	detail, err := store.GetMessage(ctx, first[0].MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WipeAll(ctx); err != nil {
		t.Fatal(err)
	}
	replacement, err := store.StoreMessage(ctx, generationMessageInput("after@example.com", "replacement"))
	if err != nil {
		t.Fatal(err)
	}
	if replacement[0].MessageID != first[0].MessageID {
		t.Fatalf("message ID was not reused: old=%d new=%d", first[0].MessageID, replacement[0].MessageID)
	}
	beforeMark := len(log.snapshot())
	if err := store.MarkReadDetail(ctx, detail); err != nil {
		t.Fatal(err)
	}
	replacementDetail, err := store.GetMessage(ctx, replacement[0].MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if replacementDetail.Message.IsRead != 0 {
		t.Fatalf("reset replacement was marked read: %+v", replacementDetail.Message)
	}
	if afterMark := len(log.snapshot()); afterMark != beforeMark {
		t.Fatalf("stale MarkReadDetail emitted events: before=%d after=%d", beforeMark, afterMark)
	}
}

func TestReadMessageMarksWithinSingleGeneration(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T, *Store, []events.Event)
	}{
		{name: "marks unread and returns prior state", run: func(t *testing.T, store *Store, emitted []events.Event) {
			stored, err := store.StoreMessage(context.Background(), generationMessageInput("read@example.com", "single"))
			if err != nil {
				t.Fatal(err)
			}
			detail, err := store.ReadMessage(context.Background(), stored[0].MessageID)
			if err != nil {
				t.Fatal(err)
			}
			if detail == nil || detail.Message.IsRead != 0 {
				t.Fatalf("detail=%+v; expected unread snapshot", detail)
			}
			persisted, err := store.GetMessage(context.Background(), stored[0].MessageID)
			if err != nil {
				t.Fatal(err)
			}
			if persisted.Message.IsRead != 1 {
				t.Fatalf("persisted read state=%d", persisted.Message.IsRead)
			}
		}},
		{name: "already read is idempotent", run: func(t *testing.T, store *Store, emitted []events.Event) {
			stored, err := store.StoreMessage(context.Background(), generationMessageInput("read@example.com", "idempotent"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.ReadMessage(context.Background(), stored[0].MessageID); err != nil {
				t.Fatal(err)
			}
			if _, err = store.ReadMessage(context.Background(), stored[0].MessageID); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "missing is not an error", run: func(t *testing.T, store *Store, emitted []events.Event) {
			detail, err := store.ReadMessage(context.Background(), 4242)
			if err != nil || detail != nil {
				t.Fatalf("detail=%+v err=%v", detail, err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var mu sync.Mutex
			emitted := []events.Event{}
			store := openTestStore(t, WithBroadcaster(func(event events.Event) {
				mu.Lock()
				defer mu.Unlock()
				emitted = append(emitted, event)
			}))
			test.run(t, store, emitted)
			mu.Lock()
			defer mu.Unlock()
			var changes int
			for _, event := range emitted {
				if event.Type == events.TypeMessagesChanged {
					changes++
				}
			}
			switch test.name {
			case "marks unread and returns prior state", "already read is idempotent":
				if changes != 1 {
					t.Fatalf("messages changed events=%d, events=%v", changes, emitted)
				}
			case "missing is not an error":
				if changes != 0 {
					t.Fatalf("missing message emitted events=%v", emitted)
				}
			}
		})
	}
}

func TestReadMessageCannotMarkAcrossGenerations(t *testing.T) {
	log := &generationEventLog{}
	store := openTestStore(t, WithBroadcaster(log.record))
	ctx := context.Background()
	const rounds = 80
	for round := 0; round < rounds; round++ {
		if _, err := store.StoreMessage(ctx, generationMessageInput("before@example.com", "before")); err != nil {
			t.Fatalf("initial delivery in round %d: %v", round, err)
		}
		observedV2 := false
		start := make(chan struct{})
		errs := make(chan error, 2)
		var wait sync.WaitGroup
		wait.Add(2)
		go func() {
			defer wait.Done()
			<-start
			detail, err := store.ReadMessage(ctx, 1)
			if err != nil {
				errs <- err
				return
			}
			if detail != nil && detail.Message.Subject != nil && *detail.Message.Subject == "replacement-subject" {
				observedV2 = true
			}
			errs <- nil
		}()
		go func() {
			defer wait.Done()
			<-start
			if err := store.WipeAll(ctx); err != nil {
				errs <- err
				return
			}
			_, err := store.StoreMessage(ctx, generationMessageInput("after@example.com", "replacement"))
			errs <- err
		}()
		close(start)
		wait.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("read/reset churn in round %d: %v", round, err)
			}
		}
		detail, err := store.GetMessage(ctx, 1)
		if err != nil {
			t.Fatal(err)
		}
		if detail == nil {
			t.Fatalf("replacement missing in round %d", round)
		}
		if detail.Message.IsRead == 1 && !observedV2 {
			t.Fatalf("replacement was marked read without being the observed generation in round %d", round)
		}
	}
	validateConcurrentEventPayloads(t, log.snapshot())
	validateAuthoritativeGenerationState(t, store, "after@example.com", "replacement", true, true)
}
