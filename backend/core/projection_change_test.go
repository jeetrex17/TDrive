package core

import "testing"

func TestProjectionChangeSubscriptionsAreIndependentAndRemovable(t *testing.T) {
	t.Parallel()

	engine := &Engine{}
	first := make([]int64, 0, 2)
	second := make([]int64, 0, 2)
	unsubscribeFirst := engine.SubscribeProjectionChanges(func(channelID int64) {
		first = append(first, channelID)
	})
	engine.SubscribeProjectionChanges(func(channelID int64) {
		second = append(second, channelID)
	})

	engine.notifyProjectionChanged(11)
	unsubscribeFirst()
	unsubscribeFirst()
	engine.notifyProjectionChanged(22)

	if len(first) != 1 || first[0] != 11 {
		t.Fatalf("first observer channels = %v, want [11]", first)
	}
	if len(second) != 2 || second[0] != 11 || second[1] != 22 {
		t.Fatalf("second observer channels = %v, want [11 22]", second)
	}
}

func TestProjectionChangeNotificationContainsSubscriberPanic(t *testing.T) {
	t.Parallel()

	engine := &Engine{warnf: func(string, ...any) {}}
	notified := false
	engine.SubscribeProjectionChanges(func(int64) { panic("subscriber failed") })
	engine.SubscribeProjectionChanges(func(int64) { notified = true })

	engine.notifyProjectionChanged(11)

	if !notified {
		t.Fatal("healthy observer was skipped after another observer panicked")
	}
}
