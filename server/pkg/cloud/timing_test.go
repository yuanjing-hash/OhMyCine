package cloud

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestOperationTimingCollectorIsTaskScopedAndConcurrentSafe(t *testing.T) {
	first, second := NewOperationTimingCollector(), NewOperationTimingCollector()
	firstContext := WithOperationTimingCollector(context.Background(), first)
	secondContext := WithOperationTimingCollector(context.Background(), second)
	var wait sync.WaitGroup
	for index := 0; index < 100; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			RecordProviderWait(firstContext, time.Millisecond)
			RecordProviderCall(firstContext, 2*time.Millisecond)
			RecordTargetList(firstContext, 3*time.Millisecond)
			RecordBatchMutation(firstContext, 4*time.Millisecond)
			RecordDBCheckpoint(firstContext, 5*time.Millisecond)
		}()
	}
	RecordProviderCall(secondContext, 7*time.Millisecond)
	wait.Wait()

	got := first.Snapshot()
	if got.ProviderWaitCalls != 100 || got.ProviderWait != 100*time.Millisecond || got.ProviderCallCalls != 100 || got.ProviderCall != 200*time.Millisecond ||
		got.TargetListCalls != 100 || got.TargetList != 300*time.Millisecond || got.BatchMutationCalls != 100 || got.BatchMutation != 400*time.Millisecond ||
		got.DBCheckpointCalls != 100 || got.DBCheckpoint != 500*time.Millisecond {
		t.Fatalf("first snapshot=%+v", got)
	}
	isolated := second.Snapshot()
	if isolated.ProviderCallCalls != 1 || isolated.ProviderCall != 7*time.Millisecond || isolated.ProviderWaitCalls != 0 || isolated.TargetListCalls != 0 || isolated.BatchMutationCalls != 0 || isolated.DBCheckpointCalls != 0 {
		t.Fatalf("second collector received another task's timings: %+v", isolated)
	}
}

func TestOperationTimingCollectorIgnoresMissingCollectorAndNegativeDuration(t *testing.T) {
	RecordProviderCall(context.Background(), time.Second)
	collector := NewOperationTimingCollector()
	ctx := WithOperationTimingCollector(context.Background(), collector)
	RecordProviderWait(ctx, -time.Second)
	got := collector.Snapshot()
	if got.ProviderWaitCalls != 1 || got.ProviderWait != 0 {
		t.Fatalf("snapshot=%+v", got)
	}
}
