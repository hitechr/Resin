package ipquality

import (
	"encoding/json"
	"log"
	"slices"
	"time"
)

// JobStore holds disposable job snapshots in cache.db.
type JobStore interface {
	SaveJob(id string, payload []byte, startedAt time.Time) error
	DeleteJob(id string) error
	LoadJobs() ([][]byte, error)
}

// AttachJobStore restores job history before admitting new work.
// An interrupted job has no surviving queue, so its remaining work is deferred.
func (c *Coordinator) AttachJobStore(store JobStore) error {
	if store == nil {
		return nil
	}
	rows, err := store.LoadJobs()
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.jobStore = store
	var jobs []Job
	for _, payload := range rows {
		var job Job
		if json.Unmarshal(payload, &job) == nil && job.ID != "" {
			jobs = append(jobs, job)
		}
	}
	slices.SortFunc(jobs, func(a, b Job) int {
		if n := a.StartedAt.Compare(b.StartedAt); n != 0 {
			return n
		}
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	if len(jobs) > MaxJobs {
		for _, job := range jobs[:len(jobs)-MaxJobs] {
			c.deleteJob(job.ID)
		}
		jobs = jobs[len(jobs)-MaxJobs:]
	}
	for _, job := range jobs {
		if job.EndedAt == nil {
			job.Canceled = true
			job.Deferred = job.Total - job.FreshSkipped - job.Completed - job.Failed
			ended := c.now().UTC()
			job.EndedAt = &ended
			c.saveJob(&job)
		}
		restored := job
		c.jobs[job.ID] = &restored
		c.jobOrder = append(c.jobOrder, job.ID)
	}
	return nil
}

// Called with c.mu held, preserving snapshot order across workers.
func (c *Coordinator) saveJob(job *Job) {
	if c.jobStore == nil {
		return
	}
	payload, err := json.Marshal(job)
	if err == nil {
		err = c.jobStore.SaveJob(job.ID, payload, job.StartedAt)
	}
	if err != nil {
		log.Printf("[ipquality] persist job %s: %v", job.ID, err)
	}
}

func (c *Coordinator) deleteJob(id string) {
	if c.jobStore != nil {
		if err := c.jobStore.DeleteJob(id); err != nil {
			log.Printf("[ipquality] delete job %s: %v", id, err)
		}
	}
}
