package lh

import (
	"bytes"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/cespare/xxhash/v2"
	"github.com/olekukonko/ll/lx"
)

const shardCount = 32

// DefaultDedupSummaryField is the field WithDedupSummary uses when field is empty.
const DefaultDedupSummaryField = "dup"

const dedupWindowSuffix = "_w"

// Dedup drops entries whose key repeats within ttl.
//
// A suppressed entry never reaches the next handler, so its count is only final
// once the window closes; WithDedupSummary reports it as one extra entry per
// finished window. Reporting is charged per window, never per suppressed entry.
type Dedup struct {
	next         lx.Handler
	ttl          time.Duration
	cleanupEvery time.Duration
	keyFn        lx.Deduper
	maxKeys      int

	summaryField   string
	summaryEvery   time.Duration
	summaryCompact bool

	shards [shardCount]dedupShard
	done   chan struct{}
	wg     sync.WaitGroup
	once   sync.Once
}

type dedupRecord struct {
	exp   int64  // expiry, unix nanos
	dup   uint32 // suppressed in this window
	first *lx.Entry
}

type dedupReport struct {
	entry *lx.Entry
	n     uint32
	rec   *dedupRecord // non-nil when the record is untracked and reusable
}

type dedupShard struct {
	mu         sync.Mutex
	seen       map[uint64]*dedupRecord
	suppressed uint64 // guarded by mu
}

var dedupRecordPool = sync.Pool{New: func() any { return new(dedupRecord) }}

// DedupOpt configures a Dedup.
type DedupOpt func(*Dedup)

// WithDedupKeyFunc overrides the key function.
func WithDedupKeyFunc(fn func(*lx.Entry) uint64) DedupOpt {
	return func(d *Dedup) {
		d.keyFn = dedupKeyFunc(fn)
	}
}

type dedupKeyFunc func(*lx.Entry) uint64

func (f dedupKeyFunc) Calculate(e *lx.Entry) uint64 { return f(e) }

// WithDedupCleanupInterval sets how often expired keys are purged.
func WithDedupCleanupInterval(every time.Duration) DedupOpt {
	return func(d *Dedup) {
		if every > 0 {
			d.cleanupEvery = every
		}
	}
}

// WithDedupMaxKeys caps tracked keys. The opportunistic purge runs on the slow
// path and skips records that still hold an unreported count, so the cap is soft.
func WithDedupMaxKeys(max int) DedupOpt {
	return func(d *Dedup) {
		if max > 0 {
			d.maxKeys = max
		}
	}
}

// WithDedupSummary reports suppressed duplicates when a window closes: one extra
// entry carrying field (default "dup") and "<field>_w", reusing the first
// occurrence's level, namespace, message and fields. One allocation per window;
// unset, no extra entries.
func WithDedupSummary(field string) DedupOpt {
	return func(d *Dedup) {
		if field == "" {
			field = DefaultDedupSummaryField
		}
		d.summaryField = field
	}
}

// WithDedupSummaryInterval sets how often finished windows are reported.
// Defaults to the smaller of the cleanup interval and ttl.
func WithDedupSummaryInterval(every time.Duration) DedupOpt {
	return func(d *Dedup) {
		if every > 0 {
			d.summaryEvery = every
		}
	}
}

// WithDedupSummaryCompact keeps only the scalars of the first occurrence in the
// summary, dropping its fields and the per-window clone allocation.
func WithDedupSummaryCompact() DedupOpt {
	return func(d *Dedup) {
		d.summaryCompact = true
	}
}

// WithDedupIgnore excludes fields from the default key.
func WithDedupIgnore(fields ...string) DedupOpt {
	return func(d *Dedup) {
		if dd, ok := d.keyFn.(*defaultDedup); ok {
			if dd.ignoreFields == nil {
				dd.ignoreFields = make(map[string]struct{}, len(fields))
			}
			for _, f := range fields {
				dd.ignoreFields[f] = struct{}{}
			}
		}
	}
}

// NewDedup wraps next with deduplication over ttl.
func NewDedup(next lx.Handler, ttl time.Duration, opts ...DedupOpt) *Dedup {
	if ttl <= 0 {
		ttl = 2 * time.Second
	}
	d := &Dedup{
		next:         next,
		ttl:          ttl,
		cleanupEvery: time.Minute,
		keyFn:        NewDefaultDedup(),
		done:         make(chan struct{}),
	}
	for i := 0; i < len(d.shards); i++ {
		d.shards[i].seen = make(map[uint64]*dedupRecord, 64)
	}
	for _, opt := range opts {
		opt(d)
	}
	if d.summaryField != "" {
		if d.summaryEvery <= 0 {
			d.summaryEvery = d.ttl
			if d.cleanupEvery < d.summaryEvery {
				d.summaryEvery = d.cleanupEvery
			}
		}
		d.cleanupEvery = d.summaryEvery
	}
	d.wg.Add(1)
	go d.cleanupLoop()
	return d
}

// Handle suppresses entries whose key was seen within ttl.
//
// The key is computed before anything is mutated, so an injected summary field
// can never change the key of a later entry.
func (d *Dedup) Handle(e *lx.Entry) error {
	if d.keyFn == nil {
		return d.next.Handle(e)
	}

	now := time.Now().UnixNano()
	key := d.keyFn.Calculate(e)

	// shardCount is a power of two.
	shard := &d.shards[key&(shardCount-1)]

	var finished *dedupRecord

	shard.mu.Lock()
	rec, ok := shard.seen[key]
	if ok && now < rec.exp {
		// One map lookup plus two in-place increments: no allocation, no
		// re-hash. The counter is per shard and bumped under the held lock; a
		// shared atomic would put every shard on one cache line.
		rec.dup++
		shard.suppressed++
		shard.mu.Unlock()
		return nil
	}

	if ok {
		if rec.dup > 0 && d.summaryField != "" {
			delete(shard.seen, key)
			finished = rec
		} else {
			d.resetRecordLocked(rec, e, now)
		}
	} else {
		if d.maxKeys > 0 {
			if limit := d.maxKeys / shardCount; limit > 0 && len(shard.seen) >= limit {
				d.purgeShardLocked(shard, now)
			}
		}
		shard.seen[key] = d.newRecordLocked(e, now)
	}
	shard.mu.Unlock()

	// Emitting formats and writes; never do it under a shard lock.
	if finished != nil {
		d.emitSummary(finished.first, finished.dup)
		d.putRecord(finished)
	}

	return d.next.Handle(e)
}

// getShardIndex returns the shard index for a given key.
func (d *Dedup) getShardIndex(key uint64) int {
	return int(key & (shardCount - 1))
}

// DedupStats is a snapshot of a Dedup's counters.
type DedupStats struct {
	Suppressed uint64 // entries dropped since creation
	Tracked    int    // keys tracked, live or awaiting a report
	Pending    uint64 // counted but not yet reported
}

// Suppressed returns the lifetime number of dropped entries. It sums one counter
// per shard, so use it for periodic reporting, not per entry.
func (d *Dedup) Suppressed() uint64 {
	var total uint64
	for i := 0; i < len(d.shards); i++ {
		shard := &d.shards[i]
		shard.mu.Lock()
		total += shard.suppressed
		shard.mu.Unlock()
	}
	return total
}

// Stats returns a snapshot of the counters.
func (d *Dedup) Stats() DedupStats {
	var s DedupStats
	for i := 0; i < len(d.shards); i++ {
		shard := &d.shards[i]
		shard.mu.Lock()
		s.Suppressed += shard.suppressed
		s.Tracked += len(shard.seen)
		for _, rec := range shard.seen {
			s.Pending += uint64(rec.dup)
		}
		shard.mu.Unlock()
	}
	return s
}

// Report emits the totals counted so far and resets them. Finished windows are
// forgotten; live windows keep suppressing for the rest of their ttl.
// No-op unless WithDedupSummary is enabled.
func (d *Dedup) Report() {
	if d.summaryField == "" {
		return
	}

	now := time.Now().UnixNano()
	var reports []dedupReport

	for i := 0; i < len(d.shards); i++ {
		shard := &d.shards[i]
		shard.mu.Lock()
		for k, rec := range shard.seen {
			if now > rec.exp {
				delete(shard.seen, k)
				if rec.dup > 0 {
					reports = append(reports, dedupReport{entry: rec.first, n: rec.dup, rec: rec})
				} else {
					d.putRecord(rec)
				}
				continue
			}
			if rec.dup == 0 {
				continue
			}
			reports = append(reports, dedupReport{entry: rec.first, n: rec.dup})
			rec.dup = 0
		}
		shard.mu.Unlock()
	}

	d.flushReports(reports)
}

// Flush reports pending totals, then flushes the handlers behind them.
func (d *Dedup) Flush() {
	d.Report()
	flushDownstream(d.next)
}

func flushDownstream(h lx.Handler) {
	if h == nil {
		return
	}
	if w, ok := h.(lx.Walker); ok {
		w.Walk(func(child lx.Handler) { flushDownstream(child) })
		return
	}
	if f, ok := h.(lx.Flusher); ok {
		f.Flush()
	}
}

// Close reports pending totals, stops the sweep goroutine and closes next.
func (d *Dedup) Close() error {
	var err error
	d.once.Do(func() {
		close(d.done)
		d.wg.Wait()
		d.Report() // before the sinks go away
		if c, ok := d.next.(interface{ Close() error }); ok {
			err = c.Close()
		}
	})
	return err
}

func (d *Dedup) cleanupLoop() {
	defer d.wg.Done()
	ticker := time.NewTicker(d.cleanupEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			d.sweep()
		case <-d.done:
			return
		}
	}
}

// sweep expires finished windows and reports their totals, after releasing every
// shard lock so a slow sink cannot stall the logging path.
func (d *Dedup) sweep() {
	now := time.Now().UnixNano()
	var reports []dedupReport

	for i := 0; i < len(d.shards); i++ {
		shard := &d.shards[i]
		shard.mu.Lock()
		for k, rec := range shard.seen {
			if now <= rec.exp {
				continue
			}
			delete(shard.seen, k)
			if rec.dup > 0 && d.summaryField != "" {
				reports = append(reports, dedupReport{entry: rec.first, n: rec.dup, rec: rec})
				continue
			}
			d.putRecord(rec)
		}
		shard.mu.Unlock()
	}

	d.flushReports(reports)
}

func (d *Dedup) flushReports(reports []dedupReport) {
	for _, r := range reports {
		d.emitSummary(r.entry, r.n)
		if r.rec != nil {
			d.putRecord(r.rec)
		}
	}
}

// emitSummary forwards one summary entry. It goes straight to next rather than
// through Handle so a summary can never be deduplicated away.
func (d *Dedup) emitSummary(e *lx.Entry, n uint32) {
	if d.summaryField == "" || n == 0 || e == nil {
		return
	}

	// Shallow copy of an entry this handler owns; Merge allocates, so neither
	// the copy nor the original's field slice is written to.
	summary := *e
	summary.Timestamp = time.Now()
	// Text and colorized handlers print ClassRaw as a bare message, dropping
	// the fields, so summaries are always ClassText.
	summary.Class = lx.ClassText
	summary.Fields = e.Fields.Merge(lx.Fields{
		{Key: d.summaryField, Value: n},
		{Key: d.summaryField + dedupWindowSuffix, Value: d.ttl.String()},
	})

	_ = d.next.Handle(&summary)
}

// purgeShardLocked recycles expired records without an unreported count (caller
// holds the lock). Records holding a count are left to the sweep, which reports
// them before forgetting them.
func (d *Dedup) purgeShardLocked(shard *dedupShard, now int64) {
	for k, rec := range shard.seen {
		if now <= rec.exp {
			continue
		}
		if rec.dup > 0 && d.summaryField != "" {
			continue
		}
		delete(shard.seen, k)
		d.putRecord(rec)
	}
}

// newRecordLocked allocates a pooled record for a new window (caller holds the
// lock). The clone is made under the lock because the sweep reads rec.first
// under that same lock.
func (d *Dedup) newRecordLocked(e *lx.Entry, now int64) *dedupRecord {
	rec := dedupRecordPool.Get().(*dedupRecord)
	d.resetRecordLocked(rec, e, now)
	return rec
}

// resetRecordLocked reopens a record in place, so a recurring key needs no map
// write and no allocation.
func (d *Dedup) resetRecordLocked(rec *dedupRecord, e *lx.Entry, now int64) {
	rec.exp = now + d.ttl.Nanoseconds()
	rec.dup = 0
	if d.summaryField != "" {
		rec.first = d.cloneForSummary(e)
	} else {
		rec.first = nil
	}
}

// cloneForSummary copies what a summary needs: the logger recycles e and its
// field slice as soon as Handle returns.
func (d *Dedup) cloneForSummary(e *lx.Entry) *lx.Entry {
	if d.summaryCompact {
		return &lx.Entry{
			Timestamp: e.Timestamp,
			Level:     e.Level,
			Message:   e.Message,
			Namespace: e.Namespace,
			Style:     e.Style,
			Class:     e.Class,
			Caller:    e.Caller,
		}
	}
	clone := CloneEntry(e)
	clone.Stack = nil // belongs to the original line, and can be large
	return clone
}

func (d *Dedup) putRecord(rec *dedupRecord) {
	if rec == nil {
		return
	}
	rec.exp = 0
	rec.dup = 0
	rec.first = nil
	dedupRecordPool.Put(rec)
}

// defaultDedup is the default key function.
type defaultDedup struct {
	ignoreFields map[string]struct{}
}

// NewDefaultDedup returns the default key function: level, message, namespace
// and the remaining fields, hashed with xxhash.
func NewDefaultDedup() lx.Deduper {
	return &defaultDedup{ignoreFields: make(map[string]struct{})}
}

// Calculate hashes the entry without reflection or per-call allocation.
//
// Fields are written in key order: entries that differ only in the order their
// fields were supplied must share a key. Duplicate keys are kept in caller order
// rather than collapsed.
func (d *defaultDedup) Calculate(e *lx.Entry) uint64 {
	bp := dedupBytesPool.Get().(*[]byte)
	b := (*bp)[:0]

	b = append(b, e.Level.String()...)
	b = append(b, 0)
	b = append(b, e.Message...)
	b = append(b, 0)
	b = append(b, e.Namespace...)
	b = append(b, 0)

	if len(e.Fields) > 0 {
		sp := dedupFieldsPool.Get().(*lx.Fields)
		fields := append((*sp)[:0], e.Fields...)

		if len(d.ignoreFields) > 0 {
			kept := fields[:0]
			for _, f := range fields {
				if _, skip := d.ignoreFields[f.Key]; !skip {
					kept = append(kept, f)
				}
			}
			fields = kept
		}
		sortDedupFields(fields)
		for _, f := range fields {
			b = appendDedupField(b, f)
		}

		*sp = fields[:0]
		dedupFieldsPool.Put(sp)
	}

	sum := xxhash.Sum64(b)
	*bp = b[:0]
	dedupBytesPool.Put(bp)
	return sum
}

// sortDedupFields orders fields by key, stably. Entries carry a handful of
// fields, so insertion sort beats the generic path.
func sortDedupFields(fields lx.Fields) {
	if len(fields) < 2 {
		return
	}
	if len(fields) > 8 {
		slices.SortStableFunc(fields, func(a, b lx.Field) int {
			switch {
			case a.Key < b.Key:
				return -1
			case a.Key > b.Key:
				return 1
			}
			return 0
		})
		return
	}
	for i := 1; i < len(fields); i++ {
		f := fields[i]
		j := i - 1
		for j >= 0 && fields[j].Key > f.Key {
			fields[j+1] = fields[j]
			j--
		}
		fields[j+1] = f
	}
}

// appendDedupField appends "key=value\x00", formatting the common scalar types
// inline and falling back to the shared formatter for anything else.
func appendDedupField(b []byte, f lx.Field) []byte {
	b = append(b, f.Key...)
	b = append(b, '=')

	switch v := f.Value.(type) {
	case string:
		b = append(b, v...)
	case int:
		b = strconv.AppendInt(b, int64(v), 10)
	case int64:
		b = strconv.AppendInt(b, v, 10)
	case int32:
		b = strconv.AppendInt(b, int64(v), 10)
	case uint64:
		b = strconv.AppendUint(b, v, 10)
	case uint32:
		b = strconv.AppendUint(b, uint64(v), 10)
	case float64:
		b = strconv.AppendFloat(b, v, 'g', -1, 64)
	case bool:
		if v {
			b = append(b, "true"...)
		} else {
			b = append(b, "false"...)
		}
	default:
		fb := dedupBufPool.Get().(*bytes.Buffer)
		fb.Reset()
		writeFieldValue(fb, f.Value)
		b = append(b, fb.Bytes()...)
		fb.Reset()
		dedupBufPool.Put(fb)
	}

	return append(b, 0)
}

var (
	dedupBufPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}
	// Pointers, not values: pooling a slice boxes its header on the heap.
	dedupBytesPool = sync.Pool{New: func() any {
		b := make([]byte, 0, 160)
		return &b
	}}
	dedupFieldsPool = sync.Pool{New: func() any {
		f := make(lx.Fields, 0, 8)
		return &f
	}}
)
