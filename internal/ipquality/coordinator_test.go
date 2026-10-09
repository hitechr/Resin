package ipquality

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func awaitJob(t *testing.T, c *Coordinator, id string) Job {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		job, ok := c.Status(id)
		if !ok {
			t.Fatal("job disappeared")
		}
		if job.EndedAt != nil {
			return job
		}
		select {
		case <-deadline:
			t.Fatal("job did not complete")
		case <-time.After(time.Millisecond):
		}
	}
}

func TestCoordinatorRevalidatesBackgroundAfterRateWait(t *testing.T) {
	var checks atomic.Int32
	var calls atomic.Int32
	s := NewService(lookupFunc(func(_ context.Context, ip string) (Result, error) {
		calls.Add(1)
		return Result{IP: ip}, nil
	}))
	s.limiter = rate.NewLimiter(rate.Every(30*time.Millisecond), 1)
	if !s.limiter.Allow() {
		t.Fatal("failed to reserve initial provider budget")
	}
	c := NewCoordinator(s, func(string) bool { return checks.Add(1) == 1 })
	defer c.Stop()
	if !c.EnqueueBackground("8.8.8.8") {
		t.Fatal("background admission")
	}
	deadline := time.After(2 * time.Second)
	for {
		if stats := c.QueueStatus(); stats.Pending == 0 && stats.BackgroundDeferred == 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("work did not finish: checks=%d, calls=%d", checks.Load(), calls.Load())
		case <-time.After(time.Millisecond):
		}
	}
	if checks.Load() != 2 || calls.Load() != 0 {
		t.Fatalf("must revalidate after waiting: checks=%d, calls=%d", checks.Load(), calls.Load())
	}
}

func TestCoordinatorPromotesManualJoinOnBackgroundRetry(t *testing.T) {
	started := make(chan string, 3)
	releaseFirst := make(chan struct{})
	var attempts atomic.Int32
	s := NewService(lookupFunc(func(ctx context.Context, ip string) (Result, error) {
		started <- ip
		if ip == "8.8.8.8" {
			if attempts.Add(1) == 1 {
				<-releaseFirst
				return Result{}, errors.New("provider temporarily unavailable")
			}
			return Result{IP: ip}, nil
		}
		<-ctx.Done()
		return Result{}, ctx.Err()
	}))
	s.limiter = rate.NewLimiter(1000, 3)
	c := NewCoordinator(s, nil)
	defer c.Stop()
	c.EnqueueBackground("8.8.8.8")
	if got := <-started; got != "8.8.8.8" {
		t.Fatalf("first start: %s", got)
	}
	c.EnqueueBackground("1.1.1.1")
	job, err := c.StartManual([]string{"8.8.8.8"})
	if err != nil {
		t.Fatal(err)
	}
	close(releaseFirst)
	if got := <-started; got != "1.1.1.1" {
		t.Fatalf("queued background start: %s", got)
	}
	select {
	case got := <-started:
		if got != "8.8.8.8" {
			t.Fatalf("manual retry: %s", got)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("manual retry remained blocked behind background work")
	}
	if final := awaitJob(t, c, job.ID); final.Completed != 1 {
		t.Fatalf("manual retry result: %+v", final)
	}
}

func TestCoordinatorCancelOneOfTwoJobsKeepsSharedLookup(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	s := NewService(lookupFunc(func(ctx context.Context, ip string) (Result, error) {
		calls.Add(1)
		close(started)
		select {
		case <-release:
			return Result{IP: ip}, nil
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	}))
	c := NewCoordinator(s, nil)
	defer c.Stop()
	first, err := c.StartManual([]string{"8.8.8.8"})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	second, err := c.StartManual([]string{"8.8.8.8"})
	if err != nil || !c.Cancel(first.ID) {
		t.Fatalf("second admission / first cancellation: %v", err)
	}
	close(release)
	if job := awaitJob(t, c, second.ID); job.Completed != 1 || calls.Load() != 1 {
		t.Fatalf("shared lookup: %+v, calls=%d", job, calls.Load())
	}
	if _, found, fresh := s.Cached("8.8.8.8"); !found || !fresh {
		t.Fatal("cancellation removed successful shared cache")
	}
}

func TestCoordinatorManualStartsDuringBlockedBackground(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{}, 2)
	s := NewService(lookupFunc(func(ctx context.Context, ip string) (Result, error) {
		started <- ip
		select {
		case <-release:
			return Result{IP: ip}, nil
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	}))
	s.limiter = rate.NewLimiter(1000, 2)
	c := NewCoordinator(s, func(string) bool { return true })
	defer c.Stop()
	c.EnqueueBackground("8.8.8.8")
	if got := <-started; got != "8.8.8.8" {
		t.Fatalf("background start: %s", got)
	}
	job, err := c.StartManual([]string{"1.1.1.1"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-started:
		if got != "1.1.1.1" {
			t.Fatalf("manual start: %s", got)
		}
	case <-time.After(time.Second):
		t.Fatal("manual work was blocked behind background")
	}
	release <- struct{}{}
	release <- struct{}{}
	if final := awaitJob(t, c, job.ID); final.Completed != 1 {
		t.Fatalf("manual job: %+v", final)
	}
}

func TestCoordinatorDeduplicatesAndSkipsFresh(t *testing.T) {
	var calls atomic.Int32
	s := NewService(lookupFunc(func(_ context.Context, ip string) (Result, error) {
		calls.Add(1)
		return Result{IP: ip}, nil
	}))
	s.limiter = rate.NewLimiter(1000, 2)
	c := NewCoordinator(s, func(ip string) bool { return true })
	defer c.Stop()
	job, err := c.StartManual([]string{"8.8.8.8", "::ffff:8.8.8.8", "1.1.1.1", "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	final := awaitJob(t, c, job.ID)
	if final.Total != 2 || final.Completed != 2 || calls.Load() != 2 {
		t.Fatalf("job=%+v, requests=%d", final, calls.Load())
	}
	job, err = c.StartManual([]string{"8.8.8.8", "1.1.1.1"})
	if err != nil {
		t.Fatal(err)
	}
	final = awaitJob(t, c, job.ID)
	if final.FreshSkipped != 2 || final.Completed != 0 || calls.Load() != 2 {
		t.Fatalf("cached job=%+v, requests=%d", final, calls.Load())
	}
}

func TestCoordinatorPrioritizesManualOverQueuedBackground(t *testing.T) {
	started := make(chan string, 4)
	release := make(chan struct{}, 4)
	s := NewService(lookupFunc(func(ctx context.Context, ip string) (Result, error) {
		started <- ip
		select {
		case <-release:
			return Result{IP: ip}, nil
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	}))
	s.limiter = rate.NewLimiter(1000, 2)
	c := NewCoordinator(s, func(string) bool { return true })
	defer c.Stop()
	if !c.EnqueueBackground("8.8.8.8") || !c.EnqueueBackground("1.1.1.1") || !c.EnqueueBackground("9.9.9.9") {
		t.Fatal("background admission")
	}
	if got := <-started; got != "8.8.8.8" {
		t.Fatalf("first background start: %s", got)
	}
	job, err := c.StartManual([]string{"4.4.4.4"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case next := <-started:
		if next != "4.4.4.4" {
			t.Fatalf("manual job should start before queued background, got %s", next)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("manual request did not start")
	}
	for range 4 {
		release <- struct{}{}
	}
	final := awaitJob(t, c, job.ID)
	if final.Completed != 1 {
		t.Fatalf("manual job: %+v", final)
	}
}

func TestCoordinatorManualJoinSurvivesBackgroundMembershipChange(t *testing.T) {
	checking := make(chan struct{})
	resume := make(chan struct{})
	var calls atomic.Int32
	s := NewService(lookupFunc(func(_ context.Context, ip string) (Result, error) {
		calls.Add(1)
		return Result{IP: ip}, nil
	}))
	c := NewCoordinator(s, func(string) bool {
		close(checking)
		<-resume
		return false
	})
	defer c.Stop()
	if !c.EnqueueBackground("8.8.8.8") {
		t.Fatal("background admission")
	}
	<-checking
	job, err := c.StartManual([]string{"8.8.8.8"})
	if err != nil {
		t.Fatal(err)
	}
	close(resume)
	final := awaitJob(t, c, job.ID)
	if final.Completed != 1 || calls.Load() != 1 {
		t.Fatalf("manual join should still check: %+v, calls=%d", final, calls.Load())
	}
}

func TestCoordinatorCancelQueuedWorkAndKeepCache(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})
	s := NewService(lookupFunc(func(ctx context.Context, ip string) (Result, error) {
		started <- ip
		select {
		case <-release:
			return Result{IP: ip}, nil
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	}))
	s.limiter = rate.NewLimiter(1000, 2)
	c := NewCoordinator(s, func(ip string) bool { return true })
	defer c.Stop()
	job, err := c.StartManual([]string{"8.8.8.8", "1.1.1.1", "9.9.9.9"})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	<-started
	if !c.Cancel(job.ID) {
		t.Fatal("cancel should find job")
	}
	close(release)
	final := awaitJob(t, c, job.ID)
	if !final.Canceled || final.Deferred == 0 {
		t.Fatalf("canceled job: %+v", final)
	}
	select {
	case ip := <-started:
		t.Fatalf("canceled queued IP started: %s", ip)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestCoordinatorReportsDeferredBackgroundAdmission(t *testing.T) {
	started := make(chan struct{})
	s := NewService(lookupFunc(func(ctx context.Context, ip string) (Result, error) {
		close(started)
		<-ctx.Done()
		return Result{}, ctx.Err()
	}))
	c := NewCoordinator(s, nil)
	defer c.Stop()
	c.maxPending = 1
	if !c.EnqueueBackground("8.8.8.8") {
		t.Fatal("first IP should be admitted")
	}
	<-started
	if c.EnqueueBackground("1.1.1.1") {
		t.Fatal("second IP should be deferred")
	}
	stats := c.QueueStatus()
	if stats.Pending != 1 || stats.BackgroundDeferred != 1 {
		t.Fatalf("queue status: %+v", stats)
	}
}

func TestCoordinatorIDsDoNotRepeatAcrossInstances(t *testing.T) {
	s := NewService(lookupFunc(func(_ context.Context, ip string) (Result, error) { return Result{IP: ip}, nil }))
	first := NewCoordinator(s, nil)
	jobA, err := first.StartManual(nil)
	if err != nil {
		t.Fatal(err)
	}
	first.Stop()
	second := NewCoordinator(s, nil)
	defer second.Stop()
	jobB, err := second.StartManual(nil)
	if err != nil || jobA.ID == jobB.ID {
		t.Fatalf("restarted IDs should differ: %q / %q, %v", jobA.ID, jobB.ID, err)
	}
}

func TestCoordinatorCancelRemovesQueuedKeys(t *testing.T) {
	started := make(chan struct{}, 1)
	s := NewService(lookupFunc(func(ctx context.Context, ip string) (Result, error) {
		if ip == "8.8.8.8" {
			started <- struct{}{}
		}
		<-ctx.Done()
		return Result{}, ctx.Err()
	}))
	s.limiter = rate.NewLimiter(1000, 2)
	c := NewCoordinator(s, nil)
	defer c.Stop()
	if !c.EnqueueBackground("8.8.8.8") {
		t.Fatal("fill background worker")
	}
	<-started
	for i := 0; i < 20; i++ {
		job, err := c.StartManual([]string{"9.9.9.9"})
		if err != nil || !c.Cancel(job.ID) {
			t.Fatalf("cancel queued job: %v", err)
		}
	}
	c.mu.Lock()
	queued := len(c.manual)
	c.mu.Unlock()
	if queued != 0 {
		t.Fatalf("cancel left %d queued keys", queued)
	}
}

func TestCoordinatorRejectsOversizeAndQueueFull(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	s := NewService(lookupFunc(func(ctx context.Context, ip string) (Result, error) {
		started <- struct{}{}
		select {
		case <-release:
			return Result{IP: ip}, nil
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	}))
	s.limiter = rate.NewLimiter(1000, 2)
	c := NewCoordinator(s, nil)
	defer c.Stop()
	ips := make([]string, MaxBatchIPs+1)
	for i := range ips {
		ips[i] = fmt.Sprintf("8.1.%d.%d", i/256, i%256)
	}
	if _, err := c.StartManual(ips); !errors.Is(err, ErrBatchTooLarge) {
		t.Fatalf("batch cap: %v", err)
	}
	c.maxPending = 2
	if _, err := c.StartManual(ips[:2]); err != nil {
		t.Fatal(err)
	}
	if _, err := c.StartManual(ips[2:3]); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("queue cap: %v", err)
	}
	close(release)
}
