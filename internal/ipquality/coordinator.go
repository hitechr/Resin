package ipquality

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
)

const MaxBatchIPs = 10000
const MaxPendingIPs = 10000
const MaxJobs = 128

var ErrBatchTooLarge = errors.New("too many distinct IPs in batch")
var ErrQueueFull = errors.New("IP quality queue is full")
var ErrCoordinatorStopped = errors.New("IP quality coordinator stopped")

type Job struct {
	ID           string     `json:"id"`
	Source       string     `json:"source"`
	Total        int        `json:"total"`
	FreshSkipped int        `json:"fresh_skipped"`
	Completed    int        `json:"completed"`
	Failed       int        `json:"failed"`
	Deferred     int        `json:"deferred"`
	Canceled     bool       `json:"canceled"`
	StartedAt    time.Time  `json:"started_at"`
	EndedAt      *time.Time `json:"ended_at,omitempty"`
	ErrorSummary string     `json:"error_summary,omitempty"`
}

type queuedIP struct {
	ip         string
	jobs       map[string]struct{}
	background bool
	manual     bool
	started    bool
	attempts   int
	readyAt    time.Time
	cancel     context.CancelFunc
}

type QueueSnapshot struct {
	Pending            int   `json:"pending"`
	BackgroundDeferred int64 `json:"background_deferred"`
}

// Coordinator owns only bounded in-memory work; Service owns the shared provider budget.
// allowed revalidates current pool membership before background network work starts.
// Callbacks passed to EnqueueBackground must not wait for a provider response.
type Coordinator struct {
	service    *Service
	jobStore   JobStore
	allowed    func(string) bool
	maxPending int
	now        func() time.Time
	ctx        context.Context
	stop       context.CancelFunc
	wg         sync.WaitGroup
	wakeManual chan struct{}
	wakeWork   chan struct{}

	mu                 sync.Mutex
	pending            map[string]*queuedIP
	manual             []string
	background         []string
	jobs               map[string]*Job
	jobOrder           []string
	backgroundDeferred int64
	stopped            bool
}

func NewCoordinator(service *Service, allowed func(string) bool) *Coordinator {
	ctx, stop := context.WithCancel(context.Background())
	c := &Coordinator{
		service: service, allowed: allowed, maxPending: MaxPendingIPs, now: time.Now,
		ctx: ctx, stop: stop, wakeManual: make(chan struct{}, 1), wakeWork: make(chan struct{}, 1), pending: make(map[string]*queuedIP),
		jobs: make(map[string]*Job),
	}
	c.wg.Add(2)
	go c.worker(false)
	go c.worker(true)
	return c
}

func (c *Coordinator) signal() {
	for _, wake := range []chan struct{}{c.wakeManual, c.wakeWork} {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
}

// StartManual accepts only server-side filtered pool IPs, not client-supplied targets.
func (c *Coordinator) StartManual(raw []string) (Job, error) {
	unique := make(map[string]struct{}, len(raw))
	for _, value := range raw {
		if ip, err := PublicIP(value); err == nil {
			unique[ip] = struct{}{}
		}
	}
	if len(unique) > MaxBatchIPs {
		return Job{}, ErrBatchTooLarge
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped {
		return Job{}, ErrCoordinatorStopped
	}
	c.pruneJobs()
	if len(c.jobs) >= MaxJobs {
		return Job{}, ErrQueueFull
	}
	newWork := 0
	fresh := 0
	needsLookup := make([]string, 0, len(unique))
	for ip := range unique {
		if _, _, ok := c.fresh(ip); ok {
			fresh++
		} else {
			needsLookup = append(needsLookup, ip)
			if c.pending[ip] == nil {
				newWork++
			}
		}
	}
	if len(c.pending)+newWork > c.maxPending {
		return Job{}, ErrQueueFull
	}
	job := &Job{ID: uuid.NewString(), Source: "manual", Total: len(unique), FreshSkipped: fresh, StartedAt: c.now().UTC()}
	c.jobs[job.ID] = job
	c.jobOrder = append(c.jobOrder, job.ID)
	for _, ip := range needsLookup {
		work := c.pending[ip]
		if work == nil {
			work = &queuedIP{ip: ip, jobs: make(map[string]struct{}), manual: true}
			c.pending[ip] = work
			c.manual = append(c.manual, ip)
		} else if !work.manual {
			work.manual = true
			if !work.started {
				c.manual = append(c.manual, ip)
			}
		}
		work.jobs[job.ID] = struct{}{}
	}
	c.finishJob(job)
	c.signal()
	return *job, nil
}

// fresh runs under c.mu; the cache has its own mutex.
func (c *Coordinator) fresh(ip string) (Result, bool, bool) {
	result, found, fresh := c.service.Cached(ip)
	return result, found, found && fresh
}

func (c *Coordinator) EnqueueBackground(raw string) bool {
	ip, err := PublicIP(raw)
	if err != nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped {
		return false
	}
	if _, _, fresh := c.fresh(ip); fresh {
		return true
	}
	if work := c.pending[ip]; work != nil {
		work.background = true
		return true
	}
	if len(c.pending) >= c.maxPending {
		c.backgroundDeferred++
		return false
	}
	c.pending[ip] = &queuedIP{ip: ip, jobs: make(map[string]struct{}), background: true}
	c.background = append(c.background, ip)
	c.signal()
	return true
}

func (c *Coordinator) QueueStatus() QueueSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return QueueSnapshot{Pending: len(c.pending), BackgroundDeferred: c.backgroundDeferred}
}

func (c *Coordinator) Status(id string) (Job, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	job := c.jobs[id]
	if job == nil {
		return Job{}, false
	}
	return *job, true
}

func (c *Coordinator) removeQueued(ip string) {
	c.manual = slices.DeleteFunc(c.manual, func(value string) bool { return value == ip })
	c.background = slices.DeleteFunc(c.background, func(value string) bool { return value == ip })
}

func (c *Coordinator) Cancel(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	job := c.jobs[id]
	if job == nil || job.EndedAt != nil {
		return false
	}
	job.Canceled = true
	job.Deferred = job.Total - job.FreshSkipped - job.Completed - job.Failed
	ended := c.now().UTC()
	job.EndedAt = &ended
	c.saveJob(job)
	for ip, work := range c.pending {
		delete(work.jobs, id)
		if len(work.jobs) != 0 {
			continue
		}
		if work.background {
			if work.manual {
				work.manual = false
				if !work.started {
					c.removeQueued(ip)
					c.background = append(c.background, ip)
				}
			}
			continue
		}
		if work.cancel != nil {
			work.cancel()
		}
		delete(c.pending, ip)
		c.removeQueued(ip)
	}
	c.signal()
	return true
}

func (c *Coordinator) finishJob(job *Job) {
	if job.EndedAt == nil && job.Completed+job.Failed+job.FreshSkipped+job.Deferred >= job.Total {
		ended := c.now().UTC()
		job.EndedAt = &ended
	}
	c.saveJob(job)
}

func (c *Coordinator) pruneJobs() {
	for len(c.jobs) >= MaxJobs {
		removed := false
		for i, id := range c.jobOrder {
			if c.jobs[id].EndedAt != nil {
				delete(c.jobs, id)
				c.jobOrder = slices.Delete(c.jobOrder, i, i+1)
				c.deleteJob(id)
				removed = true
				break
			}
		}
		if !removed {
			break
		}
	}
}

func (c *Coordinator) nextWork(manualOnly bool) (*queuedIP, time.Duration) {
	wait := time.Duration(-1)
	queues := []*[]string{&c.manual}
	if !manualOnly {
		queues = append(queues, &c.background)
	}
	for _, queue := range queues {
		count := len(*queue)
		for range count {
			ip := (*queue)[0]
			*queue = (*queue)[1:]
			work := c.pending[ip]
			if work == nil || work.started || (queue == &c.manual && !work.manual) || (queue == &c.background && work.manual) {
				continue
			}
			if delay := work.readyAt.Sub(c.now()); delay > 0 {
				*queue = append(*queue, ip)
				if wait < 0 || delay < wait {
					wait = delay
				}
				continue
			}
			work.started = true
			c.signal()
			return work, 0
		}
	}
	return nil, wait
}

func (c *Coordinator) worker(manualOnly bool) {
	defer c.wg.Done()
	wake := c.wakeWork
	if manualOnly {
		wake = c.wakeManual
	}
	for {
		c.mu.Lock()
		work, wait := c.nextWork(manualOnly)
		c.mu.Unlock()
		if work == nil {
			var timer <-chan time.Time
			if wait >= 0 {
				timer = time.After(wait)
			}
			select {
			case <-c.ctx.Done():
				return
			case <-wake:
			case <-timer:
			}
			continue
		}
		if c.ctx.Err() != nil {
			return
		}
		c.mu.Lock()
		stillPending := c.pending[work.ip] == work && (work.background || len(work.jobs) > 0)
		backgroundOnly := work.background && len(work.jobs) == 0
		c.mu.Unlock()
		if !stillPending {
			c.complete(work, nil)
			continue
		}
		if backgroundOnly && c.allowed != nil && !c.allowed(work.ip) {
			c.dropUnmatchedBackground(work)
			continue
		}
		if _, _, fresh := c.fresh(work.ip); fresh {
			c.complete(work, nil)
			continue
		}
		ctx, cancel := context.WithCancel(c.ctx)
		c.mu.Lock()
		if c.pending[work.ip] != work || (!work.background && len(work.jobs) == 0) {
			cancel()
			c.mu.Unlock()
			c.complete(work, nil)
			continue
		}
		work.cancel = cancel
		c.mu.Unlock()
		_, err := c.service.CheckQueuedWhen(ctx, work.ip, func() bool {
			c.mu.Lock()
			stillPending := c.pending[work.ip] == work && (work.background || len(work.jobs) > 0)
			backgroundOnly := work.background && len(work.jobs) == 0
			c.mu.Unlock()
			return stillPending && (!backgroundOnly || c.allowed == nil || c.allowed(work.ip))
		})
		cancel()
		if errors.Is(err, ErrNoLongerEligible) {
			c.dropUnmatchedBackground(work)
			continue
		}
		c.complete(work, err)
	}
}

func (c *Coordinator) dropUnmatchedBackground(work *queuedIP) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending[work.ip] != work {
		return
	}
	if len(work.jobs) > 0 {
		work.background = false
		work.manual = true
		work.started = false
		c.manual = append(c.manual, work.ip)
		c.signal()
		return
	}
	delete(c.pending, work.ip)
	c.backgroundDeferred++
}

func (c *Coordinator) complete(work *queuedIP, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending[work.ip] != work {
		return
	}
	work.cancel = nil
	if err != nil && !errors.Is(err, ErrInvalidResponse) && c.ctx.Err() == nil && work.attempts < 2 && (work.background || len(work.jobs) > 0) {
		work.attempts++
		work.readyAt = c.now().Add(time.Duration(1<<work.attempts) * time.Second)
		work.started = false
		if work.manual {
			c.manual = append(c.manual, work.ip)
		} else {
			c.background = append(c.background, work.ip)
		}
		c.signal()
		return
	}
	delete(c.pending, work.ip)
	if work.background && err != nil {
		c.backgroundDeferred++
	}
	for id := range work.jobs {
		job := c.jobs[id]
		if job == nil || job.Canceled {
			continue
		}
		switch {
		case err == nil:
			if _, _, fresh := c.fresh(work.ip); fresh {
				job.Completed++
			} else {
				job.Deferred++
			}
		case errors.Is(err, ErrInvalidResponse):
			job.Failed++
			job.ErrorSummary = "invalid provider response"
		default:
			job.Failed++
			job.ErrorSummary = "provider unavailable"
		}
		c.finishJob(job)
	}
}

func (c *Coordinator) Stop() {
	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		return
	}
	c.stopped = true
	c.stop()
	for _, work := range c.pending {
		if work.cancel != nil {
			work.cancel()
		}
	}
	for _, job := range c.jobs {
		if job.EndedAt == nil {
			job.Canceled = true
			job.Deferred = job.Total - job.FreshSkipped - job.Completed - job.Failed
			c.finishJob(job)
		}
	}
	c.mu.Unlock()
	c.wg.Wait()
}
