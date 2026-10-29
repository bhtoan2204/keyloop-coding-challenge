package messaging_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"sync"
	"testing"
	"time"

	"keyloop-coding-challenge/internal/application/messaging"
	"keyloop-coding-challenge/internal/domain"
	"keyloop-coding-challenge/internal/domain/aggregates"
	"keyloop-coding-challenge/internal/domain/events"
	"keyloop-coding-challenge/package/eventsourcing"
)

const topic = "inventory.inventory.es_event"

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

func newSerializer(t *testing.T) eventsourcing.Serializer {
	t.Helper()
	s := eventsourcing.NewSerializer()
	if err := s.RegisterAggregate(&aggregates.VehicleInventoryAggregate{}); err != nil {
		t.Fatal(err)
	}
	return s
}

// storedEvent is an es_event row of a test stream.
type storedEvent struct {
	id        int64
	aggregate string
	version   int
	data      interface{}
}

// stream returns the events of one vehicle: stocked, then one action proposed per
// further version.
func stream(aggregate string, firstID int64, n int) []storedEvent {
	out := make([]storedEvent, 0, n)
	for v := 1; v <= n; v++ {
		var data interface{} = &events.VehicleStockedEvent{
			DealershipID: "d-001",
			VIN:          "1HGCM82633A004352",
			Make:         "Toyota",
			Model:        "Corolla",
			ModelYear:    2023,
			StockedOn:    domain.NewDate(2026, 7, 1),
		}
		if v > 1 {
			data = &events.VehicleActionProposedEvent{
				ActionID:   fmt.Sprintf("a%d", v),
				ActionType: domain.ActionHold,
			}
		}
		out = append(out, storedEvent{
			id:        firstID + int64(v) - 1,
			aggregate: aggregate,
			version:   v,
			data:      data,
		})
	}
	return out
}

func (e storedEvent) event() eventsourcing.Event {
	return eventsourcing.Event{
		ID:            e.id,
		AggregateID:   e.aggregate,
		AggregateType: "VehicleInventoryAggregate",
		Version:       e.version,
		EventType:     fmt.Sprintf("%T", e.data)[8:],
		SchemaVersion: 1,
		Data:          e.data,
		CreatedAt:     1793257395,
	}
}

// changeValue renders the Debezium change event of e (JSON converter, no schema).
func (e storedEvent) changeValue(op string, schemaVersion int) []byte {
	data, _ := json.Marshal(e.data)
	value, _ := json.Marshal(map[string]interface{}{
		"before": nil,
		"after": map[string]interface{}{
			"id":             e.id,
			"aggregate_id":   e.aggregate,
			"aggregate_type": "VehicleInventoryAggregate",
			"event_type":     fmt.Sprintf("%T", e.data)[8:],
			"version":        e.version,
			"schema_version": schemaVersion,
			"data":           string(data),
			"metadata":       `{"actor_id":"manager-42","correlation_id":"req-1"}`,
			"created_at":     1793257395,
		},
		"source": map[string]interface{}{
			"snapshot": "false",
			"table":    "es_event",
			"ts_ms":    1793257395000,
		},
		"op":    op,
		"ts_ms": 1793257395123,
	})
	return value
}

func (e storedEvent) key() []byte {
	return []byte(`{"aggregate_id":"` + e.aggregate + `"}`)
}

// fakeTransport is an in-memory consumer group of one member: per-partition logs,
// committed offsets and a fetch position that Retry rewinds.
type fakeTransport struct {
	mu        sync.Mutex
	logs      map[int32][]messaging.Record
	position  map[int32]int64
	committed map[int32]int64
	pausedTil map[int32]time.Time
	wake      chan struct{}
	commits   [][]messaging.Record
	retries   []messaging.Record
	// beforeCommit, when set, runs at each Commit before the offsets are stored; an
	// error fails the commit (the records are then re-delivered after a restart).
	beforeCommit func([]messaging.Record) error
	maxPoll      int
	// pollErrs are returned, one per poll, before any records.
	pollErrs []error
	// blocked stops delivery; inFlight counts polled batches not yet Released (Kafka
	// runs rebalance callbacks only when it is 0).
	blocked  bool
	inFlight int
	// ops records the calls in order ("poll", "commit", "retry", "release").
	ops []string
}

func newFakeTransport() *fakeTransport {
	return &fakeTransport{
		logs:      make(map[int32][]messaging.Record),
		position:  make(map[int32]int64),
		committed: make(map[int32]int64),
		pausedTil: make(map[int32]time.Time),
		wake:      make(chan struct{}, 1),
		maxPoll:   100,
	}
}

func (f *fakeTransport) append(partition int32, key, value []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logs[partition] = append(f.logs[partition], messaging.Record{
		Topic:     topic,
		Partition: partition,
		Offset:    int64(len(f.logs[partition])),
		Key:       key,
		Value:     value,
	})
	f.signal()
}

func (f *fakeTransport) appendEvent(partition int32, e storedEvent) {
	f.append(partition, e.key(), e.changeValue("c", 1))
}

func (f *fakeTransport) signal() {
	select {
	case f.wake <- struct{}{}:
	default:
	}
}

// restart simulates a new group member: fetching resumes at the committed offsets.
func (f *fakeTransport) restart() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for p := range f.logs {
		f.position[p] = f.committed[p]
	}
	f.pausedTil = make(map[int32]time.Time)
}

func (f *fakeTransport) Poll(ctx context.Context) ([]messaging.PartitionBatch, error) {
	f.mu.Lock()
	if len(f.pollErrs) > 0 {
		err := f.pollErrs[0]
		f.pollErrs = f.pollErrs[1:]
		f.mu.Unlock()
		return nil, err
	}
	f.mu.Unlock()
	for {
		if batches := f.available(); len(batches) > 0 {
			return batches, nil
		}
		timer := time.NewTimer(5 * time.Millisecond) // paused partitions become due
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, nil // the poll timeout or shutdown: not an error (Transport contract)
		case <-f.wake:
		case <-timer.C:
		}
		timer.Stop()
	}
}

func (f *fakeTransport) available() []messaging.PartitionBatch {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.blocked {
		return nil
	}
	partitions := make([]int32, 0, len(f.logs))
	for p := range f.logs {
		partitions = append(partitions, p)
	}
	sort.Slice(partitions, func(i, j int) bool { return partitions[i] < partitions[j] })
	var batches []messaging.PartitionBatch
	for _, p := range partitions {
		if time.Now().Before(f.pausedTil[p]) {
			continue
		}
		log, pos := f.logs[p], f.position[p]
		if pos >= int64(len(log)) {
			continue
		}
		end := min(pos+int64(f.maxPoll), int64(len(log)))
		batches = append(batches, messaging.PartitionBatch{
			Topic:     topic,
			Partition: p,
			Records:   append([]messaging.Record(nil), log[pos:end]...),
		})
		f.position[p] = end
	}
	if len(batches) > 0 {
		f.inFlight++
		f.ops = append(f.ops, "poll")
	}
	return batches
}

func (f *fakeTransport) Commit(_ context.Context, processed []messaging.Record) error {
	if f.beforeCommit != nil {
		if err := f.beforeCommit(processed); err != nil {
			return err
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ops = append(f.ops, "commit")
	f.commits = append(f.commits, append([]messaging.Record(nil), processed...))
	for _, r := range processed {
		f.committed[r.Partition] = r.Offset + 1
	}
	return nil
}

func (f *fakeTransport) Retry(rec messaging.Record, delay time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ops = append(f.ops, "retry")
	f.retries = append(f.retries, rec)
	f.position[rec.Partition] = rec.Offset
	f.pausedTil[rec.Partition] = time.Now().Add(delay)
}

func (f *fakeTransport) Release() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.inFlight > 0 {
		f.inFlight--
		f.ops = append(f.ops, "release")
	}
}

// block stops delivery and reports whether no batch is in flight.
func (f *fakeTransport) block() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blocked = true
}

func (f *fakeTransport) idle() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.inFlight == 0
}

func (f *fakeTransport) failNextPoll(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pollErrs = append(f.pollErrs, err)
	f.signal()
}

func (f *fakeTransport) operations() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.ops...)
}
func (f *fakeTransport) Close() {}

func (f *fakeTransport) committedOffset(p int32) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.committed[p]
}

func (f *fakeTransport) retried() []messaging.Record {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]messaging.Record(nil), f.retries...)
}

// fakeProjector mirrors projection.Apply's contract in memory: per-aggregate applied
// versions, duplicates skipped, gaps rejected, and a failure leaves nothing applied
// (the transaction rolled back).
type fakeProjector struct {
	mu      sync.Mutex
	applied map[string]int
	log     []string // "aggregate:version" in apply order: the projected history
	fail    func(eventsourcing.Event) error
}

func newFakeProjector() *fakeProjector {
	return &fakeProjector{applied: make(map[string]int)}
}

func (p *fakeProjector) Apply(_ context.Context, ev eventsourcing.Event) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail != nil {
		if err := p.fail(ev); err != nil {
			return false, err
		}
	}
	current := p.applied[ev.AggregateID]
	if ev.Version <= current {
		return false, nil
	}
	if ev.Version != current+1 {
		return false, fmt.Errorf("%w: %s at %d got %d", messaging.ErrVersionGap, ev.AggregateID, current, ev.Version)
	}
	p.applied[ev.AggregateID] = ev.Version
	p.log = append(p.log, fmt.Sprintf("%s:%d", ev.AggregateID, ev.Version))
	return true, nil
}

func (p *fakeProjector) version(aggregate string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.applied[aggregate]
}

func (p *fakeProjector) history() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.log...)
}

func (p *fakeProjector) setFail(fail func(eventsourcing.Event) error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fail = fail
}

// fakeHistory is the event store for gap repair.
type fakeHistory struct {
	mu      sync.Mutex
	streams map[string][]storedEvent
	reads   int
}

func (h *fakeHistory) store(events ...storedEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.streams == nil {
		h.streams = make(map[string][]storedEvent)
	}
	for _, e := range events {
		h.streams[e.aggregate] = append(h.streams[e.aggregate], e)
	}
	for _, s := range h.streams {
		sort.Slice(s, func(i, j int) bool { return s[i].version < s[j].version })
	}
}

func (h *fakeHistory) ReadStream(_ context.Context, aggregate string, after, to int) ([]eventsourcing.Event, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.reads++
	var out []eventsourcing.Event
	for _, e := range h.streams[aggregate] {
		if e.version > after && e.version <= to {
			out = append(out, e.event())
		}
	}
	return out, nil
}

// fakeSync counts startup syncs and fails the first failures calls.
type fakeSync struct {
	mu       sync.Mutex
	calls    int
	failures int
}

func (s *fakeSync) Sync(context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.calls <= s.failures {
		return 0, fmt.Errorf("projection run in progress")
	}
	return 0, nil
}

func testConfig() messaging.Config {
	return messaging.Config{
		StartupSync:   false,
		ApplyTimeout:  5 * time.Second,
		RetryMinDelay: time.Millisecond,
		RetryMaxDelay: 20 * time.Millisecond,
		PollTimeout:   50 * time.Millisecond,
	}
}

// running is a consumer running in the background until stop.
type running struct {
	consumer *messaging.Consumer
	cancel   context.CancelFunc
	done     chan error
	stopOnce sync.Once
}

func start(t *testing.T, tr *fakeTransport, proj *fakeProjector, hist *fakeHistory, sync messaging.Synchronizer, cfg messaging.Config) *running {
	t.Helper()
	if hist == nil {
		hist = &fakeHistory{}
	}
	return run(t, messaging.NewConsumer(
		tr,
		proj,
		hist,
		sync,
		messaging.NewDecoder(newSerializer(t)),
		cfg,
		discard,
	))
}

// startWith runs a consumer over a custom projector (fault injection).
func startWith(t *testing.T, tr *fakeTransport, proj messaging.Projector, hist *fakeHistory, cfg messaging.Config) *running {
	t.Helper()
	return run(t, messaging.NewConsumer(
		tr,
		proj,
		hist,
		nil,
		messaging.NewDecoder(newSerializer(t)),
		cfg,
		discard,
	))
}

func run(t *testing.T, c *messaging.Consumer) *running {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r := &running{
		consumer: c,
		cancel:   cancel,
		done:     make(chan error, 1),
	}
	go func() { r.done <- c.Run(ctx) }()
	t.Cleanup(func() { r.stop(t) })
	return r
}

// stop cancels the consumer and waits for Run to return; later calls do nothing.
func (r *running) stop(t *testing.T) {
	t.Helper()
	r.stopOnce.Do(func() {
		r.cancel()
		select {
		case err := <-r.done:
			if err != nil {
				t.Errorf("Run: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("consumer did not stop")
		}
	})
}

// eventually waits for cond, failing the test after a deadline. It waits on an
// observable outcome, not on time: no ordering depends on the poll interval.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}
