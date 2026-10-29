package command_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"keyloop-coding-challenge/internal/application/command"
	"keyloop-coding-challenge/internal/domain"
	"keyloop-coding-challenge/internal/domain/aggregates"
	"keyloop-coding-challenge/internal/infrastructure/eventstore"
	"keyloop-coding-challenge/internal/infrastructure/eventstore/repos"
	"keyloop-coding-challenge/internal/infrastructure/referencedata"
	"keyloop-coding-challenge/package/eventsourcing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// now is 2026-10-29 in London and 2026-10-30 in Sydney.
var now = time.Date(2026, 10, 29, 14, 0, 0, 0, time.UTC)

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

// hookStore wraps the real store; before each save it can run a competing command or
// return an injected error, so retries are exercised against real CAS conflicts.
type hookStore struct {
	command.VehicleStore
	beforeSave func(call int) error
	saves      int
}

func (h *hookStore) SaveWithMetadata(ctx context.Context, agg eventsourcing.Aggregate, md map[string]interface{}) error {
	h.saves++
	if h.beforeSave != nil {
		if err := h.beforeSave(h.saves); err != nil {
			return err
		}
	}
	return h.VehicleStore.SaveWithMetadata(ctx, agg, md)
}

type fixture struct {
	db    *gorm.DB
	store *hookStore
	svc   *command.Service
	// plain is a second service on the same database without hooks: the "other user".
	plain *command.Service
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(sqlite.Open("file:"+name+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := repos.Migrate(db); err != nil {
		t.Fatal(err)
	}
	serializer := eventsourcing.NewSerializer()
	if err := serializer.RegisterAggregate(&aggregates.VehicleInventoryAggregate{}); err != nil {
		t.Fatal(err)
	}
	base := eventstore.NewAggregateStoreWithSnapshotInterval(repos.New(db, serializer), 0)
	hooks := &hookStore{VehicleStore: base}
	dir := referencedata.NewDealerships(db)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return &fixture{
		db: db, store: hooks,
		svc:   command.NewService(hooks, dir, fixedClock{now}, log),
		plain: command.NewService(base, dir, fixedClock{now}, log),
	}
}

var meta = command.Meta{ActorID: "manager-42", CorrelationID: "req-1"}

// stock creates a vehicle in d-001 that is ageDays old at the London business date.
func (f *fixture) stock(t *testing.T, id string, ageDays int) {
	t.Helper()
	_, err := f.plain.StockVehicle(context.Background(), command.StockVehicle{
		DealershipID: "d-001", VehicleID: id, VIN: "1HGCM82633A004352", Make: "Toyota", Model: "Corolla",
		ModelYear: 2023, StockedOn: domain.NewDate(2026, 10, 29).AddDays(-ageDays),
	}, meta)
	if err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) eventTypes(t *testing.T, id string) []string {
	t.Helper()
	var types []string
	if err := f.db.Raw(`SELECT event_type FROM es_event WHERE aggregate_id = ? ORDER BY version`, id).Scan(&types).Error; err != nil {
		t.Fatal(err)
	}
	return types
}

func target(id string) command.Target { return command.Target{DealershipID: "d-001", VehicleID: id} }

func TestStockVehicle(t *testing.T) {
	f := newFixture(t)
	res, err := f.svc.StockVehicle(context.Background(), command.StockVehicle{
		DealershipID: "d-001", VIN: "1hgcm82633a004352", Make: "Toyota", Model: "Corolla",
		ModelYear: 2023, StockedOn: domain.NewDate(2026, 6, 1),
	}, meta)
	if err != nil {
		t.Fatal(err)
	}
	v := res.Vehicle
	if v.VehicleID() == "" || v.Version() != 1 || v.IsUnsaved() || v.DealershipID() != "d-001" || v.VIN() != "1HGCM82633A004352" {
		t.Fatalf("unexpected committed state: id=%q version=%d unsaved=%v", v.VehicleID(), v.Version(), v.IsUnsaved())
	}
	if res.BusinessDate != domain.NewDate(2026, 10, 29) || !res.Changed {
		t.Fatalf("business date %s changed=%v", res.BusinessDate, res.Changed)
	}
	var md string
	if err := f.db.Raw(`SELECT metadata FROM es_event WHERE aggregate_id = ?`, v.VehicleID()).Scan(&md).Error; err != nil {
		t.Fatal(err)
	}
	if md != `{"actor_id":"manager-42","correlation_id":"req-1"}` {
		t.Fatalf("metadata %s", md)
	}
}

func TestStockVehicleRejections(t *testing.T) {
	f := newFixture(t)
	f.stock(t, "v1", 10)
	cases := []struct {
		name string
		cmd  command.StockVehicle
		want error
	}{
		{"unknown dealership", command.StockVehicle{DealershipID: "d-404", VIN: "1HGCM82633A004352", Make: "a", Model: "b", ModelYear: 2023, StockedOn: domain.NewDate(2026, 1, 1)}, command.ErrDealershipNotFound},
		{"id already used", command.StockVehicle{DealershipID: "d-001", VehicleID: "v1", VIN: "1HGCM82633A004352", Make: "a", Model: "b", ModelYear: 2023, StockedOn: domain.NewDate(2026, 1, 1)}, domain.ErrVehicleAlreadyStocked},
		{"id used by another dealership", command.StockVehicle{DealershipID: "d-002", VehicleID: "v1", VIN: "1HGCM82633A004352", Make: "a", Model: "b", ModelYear: 2023, StockedOn: domain.NewDate(2026, 1, 1)}, domain.ErrVehicleAlreadyStocked},
		{"invalid input", command.StockVehicle{DealershipID: "d-001", VIN: "short", Make: "a", Model: "b", ModelYear: 2023, StockedOn: domain.NewDate(2026, 1, 1)}, domain.ErrValidation},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := f.svc.StockVehicle(context.Background(), tc.cmd, meta); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
	if f.store.saves != 0 {
		t.Fatalf("rejected commands saved %d times", f.store.saves)
	}
}

func TestProposeActionAging(t *testing.T) {
	for _, tc := range []struct {
		age  int
		want error
	}{{89, domain.ErrVehicleNotAging}, {90, domain.ErrVehicleNotAging}, {91, nil}} {
		t.Run(fmt.Sprintf("%d days", tc.age), func(t *testing.T) {
			f := newFixture(t)
			f.stock(t, "v1", tc.age)
			res, err := f.svc.ProposeAction(context.Background(), command.ProposeAction{
				Target: target("v1"), ActionType: domain.ActionPriceReduction, Note: "5%",
			}, meta)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			if err == nil {
				a, ok := res.Vehicle.ActiveAction()
				if !ok || a.ID != res.ActionID || res.ActionID == "" || res.Vehicle.Version() != 2 {
					t.Fatalf("active=%+v actionID=%q version=%d", a, res.ActionID, res.Vehicle.Version())
				}
			}
		})
	}
}

func TestBusinessDateUsesDealershipTimeZone(t *testing.T) {
	// Stocked 2026-07-31: 90 days on the London date (2026-10-29), 91 on the Sydney date (2026-10-30).
	f := newFixture(t)
	for _, d := range []string{"d-001", "d-002"} {
		if _, err := f.plain.StockVehicle(context.Background(), command.StockVehicle{
			DealershipID: d, VehicleID: "v-" + d, VIN: "1HGCM82633A004352", Make: "Toyota", Model: "Corolla",
			ModelYear: 2023, StockedOn: domain.NewDate(2026, 7, 31),
		}, meta); err != nil {
			t.Fatal(err)
		}
	}
	propose := func(d string) error {
		_, err := f.svc.ProposeAction(context.Background(), command.ProposeAction{
			Target: command.Target{DealershipID: d, VehicleID: "v-" + d}, ActionType: domain.ActionHold,
		}, meta)
		return err
	}
	if err := propose("d-001"); !errors.Is(err, domain.ErrVehicleNotAging) {
		t.Fatalf("London: %v", err)
	}
	if err := propose("d-002"); err != nil {
		t.Fatalf("Sydney: %v", err)
	}
}

func TestCrossDealershipAccessIsNotFound(t *testing.T) {
	f := newFixture(t)
	f.stock(t, "v1", 100)
	other := command.Target{DealershipID: "d-002", VehicleID: "v1"}
	ctx := context.Background()
	checks := map[string]func() error{
		"propose": func() error {
			_, err := f.svc.ProposeAction(ctx, command.ProposeAction{Target: other, ActionType: domain.ActionHold}, meta)
			return err
		},
		"revise": func() error {
			_, err := f.svc.ReviseAction(ctx, command.ReviseAction{Target: other, ActionID: "a", ActionType: domain.ActionHold}, meta)
			return err
		},
		"cancel": func() error {
			_, err := f.svc.CancelAction(ctx, command.CancelAction{Target: other, ActionID: "a"}, meta)
			return err
		},
		"sell": func() error {
			_, err := f.svc.SellVehicle(ctx, command.SellVehicle{Target: other, SoldOn: domain.NewDate(2026, 10, 29)}, meta)
			return err
		},
		"unknown vehicle": func() error {
			_, err := f.svc.SellVehicle(ctx, command.SellVehicle{Target: target("nope"), SoldOn: domain.NewDate(2026, 10, 29)}, meta)
			return err
		},
	}
	for name, run := range checks {
		if err := run(); !errors.Is(err, command.ErrVehicleNotFound) {
			t.Errorf("%s: got %v", name, err)
		}
	}
	if f.store.saves != 0 {
		t.Fatalf("saved %d times", f.store.saves)
	}
}

func TestExpectedVersion(t *testing.T) {
	f := newFixture(t)
	f.stock(t, "v1", 100)
	stale, current := 7, 1
	_, err := f.svc.ProposeAction(context.Background(), command.ProposeAction{
		Target: command.Target{DealershipID: "d-001", VehicleID: "v1", ExpectedVersion: &stale}, ActionType: domain.ActionHold,
	}, meta)
	if !errors.Is(err, command.ErrVersionMismatch) || f.store.saves != 0 {
		t.Fatalf("stale: %v (saves=%d)", err, f.store.saves)
	}
	res, err := f.svc.ProposeAction(context.Background(), command.ProposeAction{
		Target: command.Target{DealershipID: "d-001", VehicleID: "v1", ExpectedVersion: &current}, ActionType: domain.ActionHold,
	}, meta)
	if err != nil || res.Vehicle.Version() != 2 {
		t.Fatalf("current: %v", err)
	}
}

func TestExpectedVersionIsNotRetriedAfterConflict(t *testing.T) {
	f := newFixture(t)
	f.stock(t, "v1", 100)
	f.store.beforeSave = func(int) error {
		_, err := f.plain.ProposeAction(context.Background(), command.ProposeAction{Target: target("v1"), ActionType: domain.ActionHold}, meta)
		return err
	}
	expected := 1
	_, err := f.svc.SellVehicle(context.Background(), command.SellVehicle{
		Target: command.Target{DealershipID: "d-001", VehicleID: "v1", ExpectedVersion: &expected}, SoldOn: domain.NewDate(2026, 10, 29),
	}, meta)
	if !errors.Is(err, command.ErrVersionMismatch) || f.store.saves != 1 {
		t.Fatalf("got %v after %d saves", err, f.store.saves)
	}
	if got := f.eventTypes(t, "v1"); !reflect.DeepEqual(got, []string{"VehicleStockedEvent", "VehicleActionProposedEvent"}) {
		t.Fatalf("events %v", got)
	}
}

// TestRetryRevalidatesAgainstFreshState: another user proposes an action between our
// load and our save. The retry must reload and re-run the command, which now fails the
// domain rule, instead of re-sending our stale proposal.
func TestRetryRevalidatesAgainstFreshState(t *testing.T) {
	f := newFixture(t)
	f.stock(t, "v1", 100)
	f.store.beforeSave = func(call int) error {
		if call == 1 {
			_, err := f.plain.ProposeAction(context.Background(), command.ProposeAction{Target: target("v1"), ActionType: domain.ActionAuction}, meta)
			return err
		}
		return nil
	}
	_, err := f.svc.ProposeAction(context.Background(), command.ProposeAction{Target: target("v1"), ActionType: domain.ActionHold}, meta)
	if !errors.Is(err, domain.ErrActionAlreadyActive) {
		t.Fatalf("got %v", err)
	}
	if f.store.saves != 1 {
		t.Fatalf("saves=%d: the retry must stop at the domain rule, before saving", f.store.saves)
	}
	if got := f.eventTypes(t, "v1"); !reflect.DeepEqual(got, []string{"VehicleStockedEvent", "VehicleActionProposedEvent"}) {
		t.Fatalf("events %v", got)
	}
}

func TestRetrySucceedsWhenCommandStillValid(t *testing.T) {
	f := newFixture(t)
	f.stock(t, "v1", 100)
	f.store.beforeSave = func(call int) error {
		if call == 1 {
			_, err := f.plain.ProposeAction(context.Background(), command.ProposeAction{Target: target("v1"), ActionType: domain.ActionAuction}, meta)
			return err
		}
		return nil
	}
	res, err := f.svc.SellVehicle(context.Background(), command.SellVehicle{Target: target("v1"), SoldOn: domain.NewDate(2026, 10, 29)}, meta)
	if err != nil {
		t.Fatal(err)
	}
	// The re-run sale saw the competitor's action and closed it.
	if f.store.saves != 2 || res.Vehicle.Version() != 3 || res.Vehicle.Status() != domain.StatusSold {
		t.Fatalf("saves=%d version=%d status=%s", f.store.saves, res.Vehicle.Version(), res.Vehicle.Status())
	}
}

func TestRetryIsBounded(t *testing.T) {
	f := newFixture(t)
	f.stock(t, "v1", 100)
	f.store.beforeSave = func(int) error { return fmt.Errorf("save: %w", eventstore.ErrRetryable) }
	_, err := f.svc.ProposeAction(context.Background(), command.ProposeAction{Target: target("v1"), ActionType: domain.ActionHold}, meta)
	if !eventstore.IsRetryable(err) || f.store.saves != command.MaxAttempts {
		t.Fatalf("got %v after %d saves", err, f.store.saves)
	}
}

func TestNonRetryableErrorsAreNotRetried(t *testing.T) {
	f := newFixture(t)
	f.stock(t, "v1", 100)
	// A lost connection may have committed or not: retrying could duplicate the change.
	f.store.beforeSave = func(int) error { return fmt.Errorf("commit: %w", driver.ErrBadConn) }
	_, err := f.svc.ProposeAction(context.Background(), command.ProposeAction{Target: target("v1"), ActionType: domain.ActionHold}, meta)
	if !errors.Is(err, driver.ErrBadConn) || f.store.saves != 1 {
		t.Fatalf("got %v after %d saves", err, f.store.saves)
	}
}

func TestCancelledContextStopsRetries(t *testing.T) {
	f := newFixture(t)
	f.stock(t, "v1", 100)
	ctx, cancel := context.WithCancel(context.Background())
	f.store.beforeSave = func(int) error {
		cancel()
		return eventstore.ErrConcurrencyConflict
	}
	_, err := f.svc.ProposeAction(ctx, command.ProposeAction{Target: target("v1"), ActionType: domain.ActionHold}, meta)
	if !errors.Is(err, context.Canceled) || f.store.saves != 1 {
		t.Fatalf("got %v after %d saves", err, f.store.saves)
	}
}

func TestReviseCancelSellAndNoOpRevision(t *testing.T) {
	f := newFixture(t)
	f.stock(t, "v1", 100)
	ctx := context.Background()
	proposed, err := f.svc.ProposeAction(ctx, command.ProposeAction{Target: target("v1"), ActionType: domain.ActionHold, Note: "wait"}, meta)
	if err != nil {
		t.Fatal(err)
	}
	actionID := proposed.ActionID

	saves := f.store.saves
	same, err := f.svc.ReviseAction(ctx, command.ReviseAction{Target: target("v1"), ActionID: actionID, ActionType: domain.ActionHold, Note: " wait "}, meta)
	if err != nil || same.Changed || f.store.saves != saves || same.Vehicle.Version() != 2 {
		t.Fatalf("identical revision: err=%v changed=%v saves=%d", err, same.Changed, f.store.saves-saves)
	}
	if _, err := f.svc.ReviseAction(ctx, command.ReviseAction{Target: target("v1"), ActionID: actionID, ActionType: domain.ActionAuction, Note: "lot 7"}, meta); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CancelAction(ctx, command.CancelAction{Target: target("v1"), ActionID: actionID, Reason: "buyer"}, meta); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SellVehicle(ctx, command.SellVehicle{Target: target("v1"), SoldOn: domain.NewDate(2026, 10, 29)}, meta); err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.ProposeAction(ctx, command.ProposeAction{Target: target("v1"), ActionType: domain.ActionHold}, meta)
	if !errors.Is(err, domain.ErrVehicleNotInStock) {
		t.Fatalf("after sale: %v", err)
	}
	want := []string{"VehicleStockedEvent", "VehicleActionProposedEvent", "VehicleActionRevisedEvent", "VehicleActionCancelledEvent", "VehicleSoldEvent"}
	if got := f.eventTypes(t, "v1"); !reflect.DeepEqual(got, want) {
		t.Fatalf("events %v", got)
	}
}
