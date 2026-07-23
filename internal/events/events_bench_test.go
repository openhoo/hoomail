package events

import (
	"fmt"
	"testing"
)

var benchmarkBroadcastEvent Event

func BenchmarkHubBroadcast(b *testing.B) {
	for _, fanout := range []int{1, 16, 64} {
		b.Run(fmt.Sprintf("Subscribers%d", fanout), func(b *testing.B) {
			hub := NewHub()
			subscribers := make([]<-chan Event, fanout)
			unsubscribes := make([]func(), fanout)
			for index := range fanout {
				subscribers[index], unsubscribes[index] = hub.Subscribe()
			}
			b.Cleanup(func() {
				for _, unsubscribe := range unsubscribes {
					unsubscribe()
				}
			})

			event := MessageNew(42, 101, stringPointer("Benchmark message"), stringPointer("sender@example.com"), stringPointer("Benchmark Sender"))
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			hub.Broadcast(event)

			b.StopTimer()
			for _, subscriber := range subscribers {
				select {
				case received := <-subscriber:
					benchmarkBroadcastEvent = received
				default:
					b.Fatal("subscriber did not receive broadcast")
				}
			}
			b.StartTimer()
		}
		})
	}
}

func stringPointer(value string) *string {
	return &value
}
