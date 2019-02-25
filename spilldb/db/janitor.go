package db

import (
	"context"
	"time"

	"crawshaw.io/sqlite/sqlitex"
)

// Janitor does periodic cleaning of the primary spilldb database.
type Janitor struct {
	Logf func(format string, v ...interface{})

	ctx      context.Context
	cancelFn func()
	done     chan struct{}

	pool     *sqlitex.Pool
	cleanNow chan struct{}
}

func NewJanitor(pool *sqlitex.Pool) *Janitor {
	ctx, cancelFn := context.WithCancel(context.Background())
	j := &Janitor{
		Logf:     func(format string, v ...interface{}) {},
		ctx:      ctx,
		cancelFn: cancelFn,
		done:     make(chan struct{}),
		pool:     pool,
		cleanNow: make(chan struct{}),
	}

	return j
}

func (j *Janitor) CleanNow() {
	select {
	case j.cleanNow <- struct{}{}:
	default:
	}
}

func (j *Janitor) Run() error {
	defer func() { close(j.done) }()

	t := time.NewTicker(30 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-j.ctx.Done():
			return nil
		case <-t.C:
		case <-j.cleanNow:
		}

		if err := j.clean(); err != nil {
			if err == context.Canceled {
				return nil
			}
			return nil
		}
	}
}

func (j *Janitor) Shutdown(ctx context.Context) error {
	j.cancelFn()
	<-j.done
	return nil
}

func (j *Janitor) clean() error {
	start := time.Now()

	conn := j.pool.Get(j.ctx)
	if conn == nil {
		return context.Canceled
	}
	defer j.pool.Put(conn)

	var msgsRemoved int
	defer func() {
		l := Log{
			What:     "cleanup",
			Where:    "janitor",
			When:     start,
			Duration: time.Since(start),
			Data: map[string]interface{}{
				"msgs_removed": msgsRemoved,
			},
		}
		j.Logf("%s", l)
	}()

	return nil
}
